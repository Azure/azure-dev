// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"azure.ai.training/pkg/client"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
)

const (
	// sshTunnelWSPath is the WebSocket path exposed by AML's NIP/TunDRA backend.
	// The ProxyEndpoint returned by the serviceinstances API is the base URL only;
	// this path must be appended for the upgrade request to route to sshd.
	sshTunnelWSPath = "/nbip/v1.0/ws-tcp"

	// wsHandshakeTimeout extends gorilla/websocket's default (45s) to accommodate
	// cold-path Azure ingress on freshly-Running containers.
	wsHandshakeTimeout = 60 * time.Second

	// wsReadBufSize is the chunk size used when piping stdin to the tunnel.
	wsReadBufSize = 4096

	// pingInterval is how often we send a WebSocket ping to keep the tunnel
	// alive and detect half-open TCP connections.
	pingInterval = 30 * time.Second

	// pongTimeout is the read deadline applied to the WebSocket connection.
	// Each pong (or any frame) bumps the deadline forward by this amount.
	// Set to 2x pingInterval so a single dropped pong doesn't kill the session.
	pongTimeout = 2 * pingInterval
)

// newJobSSHProxyCommand returns a hidden subcommand used internally as the
// SSH ProxyCommand. It opens a WebSocket tunnel to the SSH proxy endpoint
// and pipes stdin <-> WebSocket <-> stdout, so OpenSSH can talk to the job
// container as if it were a normal TCP connection.
func newJobSSHProxyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "_ssh-proxy <proxy-endpoint>",
		Short:  "Internal: WebSocket tunnel for SSH ProxyCommand",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			return runSSHProxy(ctx, args[0])
		},
	}
	return cmd
}

func runSSHProxy(ctx context.Context, proxyEndpoint string) error {
	if strings.TrimSpace(proxyEndpoint) == "" {
		return fmt.Errorf("proxy endpoint argument is empty")
	}

	wsURL := buildTunnelWSURL(proxyEndpoint)

	token, err := acquireARMToken(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire tunnel token: %w", err)
	}

	// sessionCtx lets the watcher goroutine close the websocket on cancellation
	// so the downstream reader unblocks promptly.
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)

	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = wsHandshakeTimeout

	conn, resp, err := dialer.DialContext(sessionCtx, wsURL, header)
	if resp != nil {
		// gorilla returns the upgrade response on failure; close its body to
		// avoid leaking the underlying TCP connection / file descriptor.
		defer resp.Body.Close()
	}
	if err != nil {
		if resp != nil {
			return fmt.Errorf("failed to open websocket tunnel (HTTP %d): %w", resp.StatusCode, err)
		}
		return fmt.Errorf("failed to open websocket tunnel: %w", err)
	}
	defer conn.Close()

	// Keepalive: refresh the read deadline on every frame (data or pong) and
	// send periodic pings. Without this a half-open TCP would block the
	// downstream goroutine in ReadMessage indefinitely.
	bumpReadDeadline := func() {
		_ = conn.SetReadDeadline(time.Now().Add(pongTimeout))
	}
	bumpReadDeadline()
	conn.SetPongHandler(func(string) error {
		bumpReadDeadline()
		return nil
	})

	// firstErr captures only the first error reported by the pipe goroutines.
	// Subsequent errors are dropped via non-blocking send so that no goroutine
	// blocks on a full channel under concurrent failure.
	firstErr := make(chan error, 1)
	sendErr := func(e error) {
		select {
		case firstErr <- e:
		default:
		}
	}

	// Watcher: when the session context is cancelled (user Ctrl+C, parent ssh
	// exit propagated via stdin EOF, or token expiry), close the underlying
	// websocket so the downstream reader unblocks. The stdin reader cannot be
	// portably unblocked (no Windows-friendly way to interrupt os.Stdin.Read);
	// however this subcommand runs as a short-lived ProxyCommand subprocess
	// per ssh session, so process exit reaps any stdin goroutine that is still
	// blocked on a blank read.
	var wg sync.WaitGroup
	wg.Go(func() {
		<-sessionCtx.Done()
		_ = conn.Close()
	})

	// Pinger: periodic WebSocket ping to keep the tunnel alive and surface
	// dead peers via the read deadline. WriteControl is documented safe to
	// call concurrently with the writer goroutine, so no mutex is needed.
	wg.Go(func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-sessionCtx.Done():
				return
			case <-ticker.C:
				if err := conn.WriteControl(
					websocket.PingMessage,
					nil,
					time.Now().Add(pingInterval),
				); err != nil {
					sendErr(err)
					return
				}
			}
		}
	})

	// Upstream: stdin -> websocket
	go func() {
		buf := make([]byte, wsReadBufSize)
		for {
			n, rerr := os.Stdin.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					sendErr(werr)
					return
				}
			}
			if rerr != nil {
				sendErr(rerr)
				return
			}
		}
	}()

	// Downstream: websocket -> stdout
	wg.Go(func() {
		for {
			_, msg, rerr := conn.ReadMessage()
			if len(msg) > 0 {
				bumpReadDeadline()
				if _, werr := os.Stdout.Write(msg); werr != nil {
					sendErr(werr)
					return
				}
			}
			if rerr != nil {
				sendErr(rerr)
				return
			}
		}
	})

	var pipeErr error
	select {
	case pipeErr = <-firstErr:
	case <-sessionCtx.Done():
		// Upstream cancellation; the watcher closes conn.
	}
	cancel()
	wg.Wait()

	// EOF and orderly close are clean exits from the user's perspective.
	// CloseAbnormalClosure (1006) is intentionally NOT treated as success so
	// that abrupt disconnects surface as a non-zero exit to OpenSSH.
	if pipeErr == nil || pipeErr == io.EOF || websocket.IsCloseError(pipeErr,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway) {
		return nil
	}
	return pipeErr
}

// buildTunnelWSURL converts an AML ProxyEndpoint URL (returned by the
// serviceinstances API as the base URL only) into the full WebSocket URL we
// must dial. Schemes are normalised to ws/wss and the NIP path is appended.
func buildTunnelWSURL(proxyEndpoint string) string {
	wsURL := proxyEndpoint
	if after, ok := strings.CutPrefix(wsURL, "https://"); ok {
		wsURL = "wss://" + after
	} else if after, ok := strings.CutPrefix(wsURL, "http://"); ok {
		wsURL = "ws://" + after
	}
	return strings.TrimRight(wsURL, "/") + sshTunnelWSPath
}

// acquireARMToken fetches a bearer token for the ARM management scope.
// Uses AzureDeveloperCLICredential to match the rest of the extension.
func acquireARMToken(ctx context.Context) (string, error) {
	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create azure credential: %w", err)
	}
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{client.ARMScope},
	})
	if err != nil {
		return "", fmt.Errorf("failed to acquire ARM token: %w", err)
	}
	return tok.Token, nil
}

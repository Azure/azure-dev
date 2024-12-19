// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/azure/azure-dev/cli/azd/test/recording"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/jsonrpc2"
)

func Test_CLI_VsServerExternalAuth(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	cli := azdcli.NewCLI(t)
	/* #nosec G204 - Subprocess launched with a potential tainted input or cmd arguments false positive */
	cmd := exec.CommandContext(ctx, cli.AzdPath, "vs-server")
	cmd.Env = append(cli.Env, os.Environ()...)
	cmd.Env = append(cmd.Env, "AZD_DEBUG_SERVER_DEBUG_ENDPOINTS=true")
	pathString := ostest.CombinedPaths(cmd.Env)
	if len(pathString) > 0 {
		cmd.Env = append(cmd.Env, pathString)
	}

	var stdout bytes.Buffer
	cmd.Stdout = io.MultiWriter(&stdout, &logWriter{initialTime: time.Now(), t: t, prefix: "[svr-out] "})
	cmd.Stderr = &logWriter{initialTime: time.Now(), t: t, prefix: "[svr-err] "}
	err := cmd.Start()
	require.NoError(t, err)

	// Wait for the server to start
	for i := 0; i < 5; i++ {
		time.Sleep(300 * time.Millisecond)
		if stdout.Len() > 0 {
			break
		}
	}

	var svr contracts.VsServerResult
	err = json.Unmarshal(stdout.Bytes(), &svr)
	require.NoError(t, err, "value: %s", stdout.String())

	ssConn, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d/ServerService/v1.0", svr.Port), nil)
	require.NoError(t, err)

	ssRpcConn := jsonrpc2.NewConn(newWebSocketStream(ssConn))
	ssRpcConn.Go(ctx, nil)

	dsConn, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d/TestDebugService/v1.0", svr.Port), nil)
	require.NoError(t, err)

	dsRpcConn := jsonrpc2.NewConn(newWebSocketStream(dsConn))
	dsRpcConn.Go(ctx, nil)

	cwd, err := os.Getwd()
	require.NoError(t, err)

	mockAuthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authToken := r.Header.Get("Authorization")
		var resultToken string

		switch authToken {
		case "Bearer test-key-1":
			resultToken = "test-token-1"
		case "Bearer test-key-2":
			resultToken = "test-token-2"
		default:
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		_, err := w.Write([]byte(fmt.Sprintf(`{"status": "success", "token": "%s", "expiresOn": "%s"}`,
			resultToken, time.Now().Add(1*time.Hour).Format(time.RFC3339))))
		require.NoError(t, err)
	}))

	defer mockAuthServer.Close()

	// Create two sessions - they both use the same external auth server, but with different
	// access keys.  The server will return a different token for each key, allowing us to confirm that
	// the access information is being scoped per session.
	var session1 any
	var session2 any

	_, err = ssRpcConn.Call(ctx, "InitializeAsync",
		[]any{cwd,
			map[string]string{
				"AuthenticationEndpoint": mockAuthServer.URL,
				"AuthenticationKey":      "test-key-1",
			},
		}, &session1)
	require.NoError(t, err)

	_, err = ssRpcConn.Call(ctx, "InitializeAsync",
		[]any{cwd,
			map[string]string{
				"AuthenticationEndpoint": mockAuthServer.URL,
				"AuthenticationKey":      "test-key-2",
			},
		}, &session2)
	require.NoError(t, err)

	// Now, fetch the tokens and ensure we get the correct one for each session.
	var token1 azcore.AccessToken
	var token2 azcore.AccessToken

	_, err = dsRpcConn.Call(ctx, "FetchTokenAsync",
		[]any{session1}, &token1)

	require.NoError(t, err)
	require.Equal(t, "test-token-1", token1.Token)

	_, err = dsRpcConn.Call(ctx, "FetchTokenAsync",
		[]any{session2}, &token2)

	require.NoError(t, err)
	require.Equal(t, "test-token-2", token2.Token)
}

func Test_CLI_VsServer(t *testing.T) {
	testDir := filepath.Join("testdata", "vs-server", "tests")
	// List all tests
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(context.Background(), "dotnet", "test", "--list-tests")
	cmd.Dir = testDir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	require.NoError(t, err, "stdout: %s, stderr: %s", stdout.String(), stderr.String())

	scanner := bufio.NewScanner(&stdout)
	testStart := false

	type vsServerTest struct {
		Name string
		// The test may use live resources requiring cleanup.
		IsLive bool
	}
	var tests []vsServerTest
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "The following Tests are available:") {
			testStart = true
			continue
		}

		if testStart && strings.HasPrefix(line, "    ") {
			test := vsServerTest{}
			name := strings.TrimSpace(line)

			test.Name = name
			if strings.HasPrefix(name, "Live") {
				test.IsLive = true
			}
			tests = append(tests, test)
		}
	}

	stdout.Reset()
	stderr.Reset()
	cmd = exec.CommandContext(context.Background(), "dotnet", "build")
	cmd.Dir = testDir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	require.NoError(t, err, "stdout: %s, stderr: %s", stdout.String(), stderr.String())

	// For each test, copySample
	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			tt := tt
			t.Parallel()

			ctx, cancel := newTestContext(t)
			defer cancel()

			dir := tempDirWithDiagnostics(t)
			t.Logf("DIR: %s", dir)

			err = copySample(dir, "aspire-full")
			require.NoError(t, err, "failed expanding sample")

			var session *recording.Session
			envName := ""
			subscriptionId := cfg.SubscriptionID

			if tt.IsLive {
				session = recording.Start(t)
				envName = randomOrStoredEnvName(session)
				subscriptionId = cfgOrStoredSubscription(session)
			}

			cli := azdcli.NewCLI(t, azdcli.WithSession(session))
			/* #nosec G204 - Subprocess launched with a potential tainted input or cmd arguments false positive */
			cmd := exec.CommandContext(ctx, cli.AzdPath, "vs-server", "--use-tls")
			cmd.Env = append(cli.Env, os.Environ()...)
			cmd.Env = append(cmd.Env, "AZD_DEBUG_SERVER_DEBUG_ENDPOINTS=true")
			pathString := ostest.CombinedPaths(cmd.Env)
			if len(pathString) > 0 {
				cmd.Env = append(cmd.Env, pathString)
			}

			if tt.IsLive {
				defer cleanupDeployments(ctx, t, cli, session, envName)
			}

			var stdout bytes.Buffer
			cmd.Stdout = io.MultiWriter(&stdout, &logWriter{initialTime: time.Now(), t: t, prefix: "[svr-out] "})
			cmd.Stderr = &logWriter{initialTime: time.Now(), t: t, prefix: "[svr-err] "}
			err = cmd.Start()
			require.NoError(t, err)

			// Wait for the server to start
			for i := 0; i < 5; i++ {
				time.Sleep(300 * time.Millisecond)
				if stdout.Len() > 0 {
					break
				}
			}

			var svr contracts.VsServerResult
			err = json.Unmarshal(stdout.Bytes(), &svr)
			require.NoError(t, err, "value: %s", stdout.String())

			/* #nosec G204 - Subprocess launched with a potential tainted input or cmd arguments false positive */
			cmd = exec.CommandContext(context.Background(),
				"dotnet", "test",
				"--no-build",
				"--no-restore",
				"--filter", "Name="+tt.Name)
			cmd.Dir = testDir
			cmd.Env = append(cmd.Env, os.Environ()...)
			cmd.Env = append(cmd.Env, "AZURE_SUBSCRIPTION_ID="+subscriptionId)
			cmd.Env = append(cmd.Env, "AZURE_LOCATION="+cfg.Location)
			cmd.Env = append(cmd.Env, fmt.Sprintf("PORT=%d", svr.Port))
			cmd.Env = append(cmd.Env, "CERTIFICATE_BYTES="+*svr.CertificateBytes)
			cmd.Env = append(cmd.Env, "ROOT_DIR="+dir)
			cmd.Env = append(cmd.Env, "APP_HOST_PATHS="+
				filepath.Join(dir, "AspireAzdTests.AppHost", "AspireAzdTests.AppHost.csproj"))
			if tt.IsLive {
				cmd.Env = append(cmd.Env, "AZURE_ENV_NAME="+envName)
			}

			cmd.Stdout = &logWriter{initialTime: time.Now(), t: t, prefix: "[t-out] "}
			cmd.Stderr = &logWriter{initialTime: time.Now(), t: t, prefix: "[t-err] "}
			err = cmd.Run()
			require.NoError(t, err)
		})
	}
}

// wsStream adapts the websocket.Conn to jsonrpc2.Stream interface
type wsStream struct {
	c *websocket.Conn
}

// Close implements jsonrpc2.Stream.
func (*wsStream) Close() error {
	return nil
}

// Read implements jsonrpc2.Stream.
func (s *wsStream) Read(ctx context.Context) (jsonrpc2.Message, int64, error) {
	mt, data, err := s.c.ReadMessage()
	if err != nil {
		return nil, 0, err
	}
	if mt != websocket.TextMessage {
		return nil, 0, fmt.Errorf("unexpected message type: %v", mt)
	}
	msg, err := jsonrpc2.DecodeMessage(data)
	if err != nil {
		return nil, 0, err
	}
	return msg, int64(len(data)), nil
}

// Write implements jsonrpc2.Stream.
func (s *wsStream) Write(ctx context.Context, msg jsonrpc2.Message) (int64, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return 0, fmt.Errorf("marshaling message: %w", err)
	}

	if err := s.c.WriteMessage(websocket.TextMessage, data); err != nil {
		return 0, err
	}

	return int64(len(data)), nil
}

func newWebSocketStream(c *websocket.Conn) *wsStream {
	return &wsStream{c: c}
}

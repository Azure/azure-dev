// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"azureaiinspector/internal/inspector"

	"github.com/cli/browser"
	"github.com/spf13/cobra"
)

const (
	// DefaultAgentPort is the default port for local agent servers.
	// Matches the default that `azd ai agent run` uses to start a local agent.
	DefaultAgentPort = 8088

	// DefaultInspectorPort is the default port for the Agent Inspector UI.
	DefaultInspectorPort = 8087
)

// inspectorFlags holds the flags accepted by the `azd ai inspector launch` command.
type inspectorFlags struct {
	port           int
	inspectorPort  int
	sessionID      string
	conversationID string
	silent         bool
}

// newLaunchCommand returns the launch command for `azd ai inspector launch`. The
// inspector is a local-only tool: it serves an embedded SPA in the user's
// default browser and proxies the SPA's HTTP/SSE traffic to a Foundry
// agent already running on `localhost:<port>` (typically started via
// `azd ai agent run`).
func newLaunchCommand() *cobra.Command {
	flags := &inspectorFlags{}

	cmd := &cobra.Command{
		Use:   "launch",
		Short: "Launch the Agent Inspector UI in a browser, pointed at a local agent.",
		Long: `Launch the Agent Inspector UI in a browser.

The inspector serves an embedded single-page application and proxies the SPA's
HTTP/SSE calls to a Foundry agent running on localhost (default port 8088).
The agent itself is started separately, for example via 'azd ai agent run'.

SSE chunks streamed to the SPA are also mirrored to your terminal so you can
watch progress without focusing the browser.`,
		Example: `  # Launch the inspector against a local agent on the default port (8088),
  # serving the UI on the default port (8087).
  azd ai inspector launch

  # Launch with custom ports.
  azd ai inspector launch --port 9000 --inspector-port 9001

  # Seed an explicit session/conversation (otherwise the SPA mints fresh UUIDs).
  azd ai inspector launch --session-id <uuid> --conversation-id <uuid>`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInspector(cmd.Context(), flags)
		},
	}

	cmd.Flags().IntVar(&flags.port, "port", DefaultAgentPort,
		fmt.Sprintf("Localhost port of the agent the inspector targets (default: %d)", DefaultAgentPort))
	cmd.Flags().IntVar(&flags.inspectorPort, "inspector-port", DefaultInspectorPort,
		fmt.Sprintf("Port the Agent Inspector UI listens on (default: %d)", DefaultInspectorPort))
	cmd.Flags().StringVar(&flags.sessionID, "session-id", "",
		"Optional explicit session ID for the SPA. If omitted, the SPA mints a fresh UUID.")
	cmd.Flags().StringVar(&flags.conversationID, "conversation-id", "",
		"Optional explicit conversation ID for the SPA. If omitted, the SPA mints a fresh UUID.")
	cmd.Flags().BoolVar(&flags.silent, "silent", false, "Suppress terminal output")
	_ = cmd.Flags().MarkHidden("silent")

	return cmd
}

// runInspector launches the inspector UI against the local agent.
func runInspector(ctx context.Context, flags *inspectorFlags) error {
	logger := log.New(log.Writer(), "[inspector] ", log.LstdFlags)
	var sseSink func(io.Reader)
	if flags.silent {
		logger = log.New(io.Discard, "", 0)
	} else {
		sseSink = func(r io.Reader) {
			if err := readSSEStream(injectSSEEvents(r), "local"); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
			}
		}
	}

	srv := inspector.New(inspector.Config{
		Port:           flags.inspectorPort,
		AgentPort:      flags.port,
		Logger:         logger,
		SessionID:      flags.sessionID,
		ConversationID: flags.conversationID,
		SSESink:        sseSink,
		Silent:         flags.silent,
	})

	url := srv.URL()
	if !flags.silent {
		fmt.Printf("Inspector:    %s\n", url)
		fmt.Printf("Target:       localhost:%d (local)\n", flags.port)
		if flags.sessionID != "" {
			fmt.Printf("Session:      %s\n", flags.sessionID)
		}
		if flags.conversationID != "" {
			fmt.Printf("Conversation: %s\n", flags.conversationID)
		}
		fmt.Println("\nPress Ctrl+C to stop the inspector.")
	}

	ready := make(chan struct{})
	openCtx, cancelOpen := context.WithCancel(ctx)
	defer cancelOpen()
	go func() {
		select {
		case <-ready:
			if err := browser.OpenURL(url); err != nil {
				logger.Printf("failed to open browser: %v", err)
			}
		case <-openCtx.Done():
		}
	}()

	return srv.Start(ctx, ready)
}

// injectSSEEvents wraps the local agentserver SSE stream so it matches the
// Foundry SSE shape that readSSEStream expects. agentserver discriminates
// chunks via a JSON `type` field on each `data:` line and omits the
// `event:` line that readSSEStream switches on; this helper synthesises it.
// `response.failed` is mapped to `response.completed` so the failed-status
// branch in readSSEStream catches it.
func injectSSEEvents(r io.Reader) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				var typed struct {
					Type string `json:"type"`
				}
				if json.Unmarshal([]byte(data), &typed) == nil && typed.Type != "" {
					event := typed.Type
					if event == "response.failed" {
						event = "response.completed"
					}
					if _, err := fmt.Fprintf(pw, "event: %s\n", event); err != nil {
						_ = pw.CloseWithError(err)
						return
					}
				}
			}
			if _, err := fmt.Fprintln(pw, line); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
		}
		if err := scanner.Err(); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.Close()
	}()
	return pr
}

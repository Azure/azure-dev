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

	"azureaiagent/internal/inspector"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/cli/browser"
)

// runInspector launches the inspector UI against the local agent.
func (a *InvokeAction) runInspector(ctx context.Context) error {
	logger := log.New(log.Writer(), "[inspector] ", log.LstdFlags)

	// Resolve persisted session/conversation IDs the same way `azd ai agent
	// invoke` does, so the inspector continues whatever chat the CLI was
	// using. --new-session / --new-conversation flags flow through.
	sessionID, conversationID := a.resolveInspectorSeedIDs(ctx, logger)

	srv := inspector.New(inspector.Config{
		Port:           a.flags.inspectorPort,
		AgentPort:      a.flags.port,
		Logger:         logger,
		SessionID:      sessionID,
		ConversationID: conversationID,
		SSESink: func(r io.Reader) {
			if err := readSSEStream(injectSSEEvents(r), "local"); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
			}
		},
	})

	url := srv.URL()
	fmt.Printf("Inspector:    %s\n", url)
	fmt.Printf("Target:       localhost:%d (local)\n", a.flags.port)
	if sessionID != "" || conversationID != "" {
		printSessionStatus("Session:      ", sessionID)
		fmt.Printf("Conversation: %s\n", conversationID)
	}
	fmt.Println("\nPress Ctrl+C to stop the inspector.")

	ready := make(chan struct{})
	go func() {
		<-ready
		if err := browser.OpenURL(url); err != nil {
			logger.Printf("failed to open browser: %v", err)
		}
	}()

	return srv.Start(ctx, ready)
}

// resolveInspectorSeedIDs reads the persisted per-agent session/conversation
// IDs from azd UserConfig (same path as `azd ai agent invoke`). Returns
// empty strings if azd is unavailable — the SPA falls back to a fresh UUID.
func (a *InvokeAction) resolveInspectorSeedIDs(ctx context.Context, logger *log.Logger) (string, string) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", ""
	}
	defer azdClient.Close()

	agentKey := resolveLocalAgentKey(ctx, azdClient, a.flags.name, a.noPrompt)

	sid, err := resolveStoredID(
		ctx, azdClient, agentKey, a.flags.session, a.flags.newSession, "sessions", true,
	)
	if err != nil {
		logger.Printf("seed session ID: %v", err)
	}
	convID, err := resolveStoredID(
		ctx, azdClient, agentKey, a.flags.conversation, a.flags.newConversation, "conversations", true,
	)
	if err != nil {
		logger.Printf("seed conversation ID: %v", err)
	}
	return sid, convID
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
		defer pw.Close()
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
					fmt.Fprintf(pw, "event: %s\n", event)
				}
			}
			fmt.Fprintln(pw, line)
		}
	}()
	return pr
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package copilot

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

// TestCopilotSDK_E2E validates azd's managed Copilot CLI and SDK client lifecycle end-to-end:
// CLI download → client start → session create → send message → receive response → cleanup.
//
// Requires: network access, GitHub Copilot authentication, GitHub Copilot subscription.
// Skip with: go test -short
func TestCopilotSDK_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	if os.Getenv("SKIP_COPILOT_E2E") == "1" {
		t.Skip("SKIP_COPILOT_E2E is set")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	// Isolate the managed CLI cache so the download path is exercised on every run.
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	t.Setenv("AZD_COPILOT_CLI_PATH", "")

	// 1. Download the pinned CLI and start it through azd's client manager.
	cli := NewCopilotCLI(mockinput.NewMockConsole(), nil, http.DefaultClient)
	clientManager := NewCopilotClientManager(&CopilotClientOptions{
		LogLevel: "error",
	}, cli)

	err := clientManager.Start(ctx)
	require.NoError(t, err, "client manager failed to download or start the Copilot CLI")
	defer func() {
		stopErr := clientManager.Stop()
		if stopErr != nil {
			t.Logf("client manager stop error: %v", stopErr)
		}
	}()
	client := clientManager.Client()
	require.NotNil(t, client)

	// 2. Check auth
	auth, err := clientManager.GetAuthStatus(ctx)
	require.NoError(t, err)
	t.Logf("Auth: authenticated=%v, login=%v", auth.IsAuthenticated, auth.Login)
	require.True(t, auth.IsAuthenticated, "not authenticated with GitHub Copilot")

	// 3. List models
	models, err := clientManager.ListModels(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, models, "no models available")
	t.Logf("Available models: %d", len(models))
	for i, m := range models {
		if i < 5 {
			t.Logf("  - %s (id=%s)", m.Name, m.ID)
		}
	}

	// 4. Create session
	session, err := client.CreateSession(ctx, &copilot.SessionConfig{
		SystemMessage: &copilot.SystemMessageConfig{
			Mode:    "replace",
			Content: "You are a helpful assistant. Answer concisely in one sentence.",
		},
		OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
	})
	require.NoError(t, err, "CreateSession failed")
	t.Logf("Session created: %s", session.WorkspacePath())
	defer func() {
		if disconnectErr := session.Disconnect(); disconnectErr != nil {
			t.Logf("session.Destroy error: %v", disconnectErr)
		}
	}()

	// 5. Collect events
	var events []copilot.SessionEvent
	unsubscribe := session.On(func(event copilot.SessionEvent) {
		events = append(events, event)
		t.Logf("Event: type=%s", event.Type())
	})
	defer unsubscribe()

	// 6. Send message and wait for response
	t.Log("Sending prompt...")
	response, err := session.SendAndWait(ctx, copilot.MessageOptions{
		Prompt: "What is 2+2? Reply with just the number.",
	})
	require.NoError(t, err, "SendAndWait failed")

	// 7. Validate response
	t.Logf("Received %d events total", len(events))
	if response != nil {
		data, ok := response.Data.(*copilot.AssistantMessageData)
		require.True(t, ok, "expected response.Data to be *copilot.AssistantMessageData, got %T", response.Data)
		t.Logf("Response content: %s", data.Content)
		require.Contains(t, data.Content, "4",
			"expected response to contain '4'")
	} else {
		// If SendAndWait returned nil, check events for assistant message
		var found bool
		for _, e := range events {
			if e.Type() == copilot.SessionEventTypeAssistantMessage {
				if data, ok := e.Data.(*copilot.AssistantMessageData); ok {
					t.Logf("Found assistant message in events: %s", data.Content)
					found = true
					break
				}
			}
		}
		if !found {
			// Log all event types for debugging
			for _, e := range events {
				detail := ""
				if data, ok := e.Data.(*copilot.AssistantMessageData); ok {
					detail = fmt.Sprintf(" content=%s", truncateForLog(data.Content, 100))
				}
				t.Logf("  event: type=%s%s", e.Type(), detail)
			}
			t.Fatal("no assistant message received")
		}
	}
}

func truncateForLog(s string, max int) string {
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

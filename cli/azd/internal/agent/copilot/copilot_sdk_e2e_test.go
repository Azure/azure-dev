// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package copilot_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal/agent"
	agentcopilot "github.com/azure/azure-dev/cli/azd/internal/agent/copilot"
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

	if os.Getenv("INCLUDE_COPILOT_E2E") != "1" {
		t.Skip("INCLUDE_COPILOT_E2E is NOT set to 1, skipping test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	// Isolate the managed CLI cache so the download path is exercised on every run.
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	t.Setenv("AZD_COPILOT_CLI_PATH", "")

	// 1. Download the pinned CLI and start it through azd's client manager.
	cli := agentcopilot.NewCopilotCLI(mockinput.NewMockConsole(), nil, http.DefaultClient)
	clientManager := agentcopilot.NewCopilotClientManager(&agentcopilot.CopilotClientOptions{
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
	t.Logf("Auth: authenticated=%v", auth.IsAuthenticated)
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
	t.Log("Session created")
	defer func() {
		if disconnectErr := session.Disconnect(); disconnectErr != nil {
			t.Logf("session.Destroy error: %v", disconnectErr)
		}
	}()

	// 5. Collect events
	collector := agent.NewHeadlessCollector()
	captured := &capturedEvents{}
	unsubscribe := session.On(func(event copilot.SessionEvent) {
		t.Logf("Event: type=%s", event.Type())
		aiuFields, err := formatAIUFields(event)
		captured.Add(event, err)
		if aiuFields != "" {
			t.Logf("AIU event: type=%s %s", event.Type(), aiuFields)
		}
		collector.HandleEvent(event)
	})
	defer unsubscribe()

	// 6. Send two messages in the same session and wait for each turn to become idle.
	t.Log("Sending first prompt...")
	firstEvent := captured.Len()
	firstResponse, err := session.SendAndWait(ctx, copilot.MessageOptions{
		Prompt: "What is 2+2? Reply with just the number.",
	})
	require.NoError(t, err, "first SendAndWait failed")
	require.NoError(t, collector.WaitForIdle(ctx), "collector did not observe first session idle")
	require.NoError(t, captured.Err())
	requireResponseContains(t, firstResponse, captured.Since(firstEvent), "4")

	firstUsage := collector.GetUsageMetrics()
	require.Positive(t, firstUsage.AICredits, "expected positive AI credit usage after first turn")

	t.Log("Sending follow-up prompt...")
	secondEvent := captured.Len()
	secondResponse, err := session.SendAndWait(ctx, copilot.MessageOptions{
		Prompt: "Add 3 to the number you answered in the previous turn. Reply with just the result.",
	})
	require.NoError(t, err, "second SendAndWait failed")
	require.NoError(t, collector.WaitForIdle(ctx), "collector did not observe second session idle")
	require.NoError(t, captured.Err())
	requireResponseContains(t, secondResponse, captured.Since(secondEvent), "7")

	usage := collector.GetUsageMetrics()
	require.Greater(t, usage.InputTokens, firstUsage.InputTokens)
	require.Greater(t, usage.OutputTokens, firstUsage.OutputTokens)
	require.Greater(t, usage.AICredits, firstUsage.AICredits)
	t.Logf("Usage: input=%v output=%v AI credits=%v", usage.InputTokens, usage.OutputTokens, usage.AICredits)

	// 7. Validate response
	t.Logf("Received %d events total", captured.Len())
}

type capturedEvents struct {
	mu     sync.Mutex
	events []copilot.SessionEvent
	err    error
}

func (c *capturedEvents) Add(event copilot.SessionEvent, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.events = append(c.events, event)
	if c.err == nil {
		c.err = err
	}
}

func (c *capturedEvents) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.events)
}

func (c *capturedEvents) Since(index int) []copilot.SessionEvent {
	c.mu.Lock()
	defer c.mu.Unlock()

	return slices.Clone(c.events[index:])
}

func (c *capturedEvents) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.err
}

func requireResponseContains(
	t *testing.T,
	response *copilot.SessionEvent,
	events []copilot.SessionEvent,
	expected string,
) {
	t.Helper()

	if response != nil {
		data, ok := response.Data.(*copilot.AssistantMessageData)
		require.True(t, ok, "expected response.Data to be *copilot.AssistantMessageData, got %T", response.Data)
		t.Logf("Response content: %s", data.Content)
		require.Contains(t, data.Content, expected)
	} else {
		var found bool
		for _, event := range events {
			if event.Type() == copilot.SessionEventTypeAssistantMessage {
				if data, ok := event.Data.(*copilot.AssistantMessageData); ok {
					t.Logf("Found assistant message in events: %s", data.Content)
					if strings.Contains(data.Content, expected) {
						found = true
						break
					}
				}
			}
		}
		if !found {
			for _, event := range events {
				detail := ""
				if data, ok := event.Data.(*copilot.AssistantMessageData); ok {
					detail = fmt.Sprintf(" content=%s", truncateForLog(data.Content, 100))
				}
				t.Logf("  event: type=%s%s", event.Type(), detail)
			}
			t.Fatalf("no assistant message containing %q received", expected)
		}
	}
}

func formatAIUFields(event copilot.SessionEvent) (string, error) {
	encoded, err := json.Marshal(event.Data)
	if err != nil {
		return "", fmt.Errorf("marshaling %s event data: %w", event.Type(), err)
	}

	var data any
	if err := json.Unmarshal(encoded, &data); err != nil {
		return "", fmt.Errorf("unmarshaling %s event data: %w", event.Type(), err)
	}

	var fields []string
	collectAIUFields(data, "data", &fields)
	if len(fields) == 0 {
		return "", nil
	}
	if usage, ok := event.Data.(*copilot.AssistantUsageData); ok {
		if usage.InputTokens != nil {
			fields = append(fields, fmt.Sprintf("data.inputTokens=%d", *usage.InputTokens))
		}
		if usage.OutputTokens != nil {
			fields = append(fields, fmt.Sprintf("data.outputTokens=%d", *usage.OutputTokens))
		}
	}

	slices.Sort(fields)
	return strings.Join(fields, ", "), nil
}

func collectAIUFields(value any, path string, fields *[]string) {
	switch value := value.(type) {
	case map[string]any:
		for name, child := range value {
			childPath := path + "." + name
			if name == "totalNanoAiu" {
				if number, ok := child.(float64); ok {
					*fields = append(*fields, fmt.Sprintf("%s=%g", childPath, number))
				}
				continue
			}
			collectAIUFields(child, childPath, fields)
		}
	case []any:
		for _, child := range value {
			collectAIUFields(child, path+"[]", fields)
		}
	}
}

func truncateForLog(s string, max int) string {
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

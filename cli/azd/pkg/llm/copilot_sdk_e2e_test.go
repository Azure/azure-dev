// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/stretchr/testify/require"
)

// TestCopilotSDK_E2E validates the Copilot SDK client lifecycle end-to-end:
// client start → session create → send message → receive response → cleanup.
//
// Requires: copilot CLI in PATH (v0.0.419+), GitHub Copilot subscription.
// Skip with: go test -short
func TestCopilotSDK_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	if os.Getenv("SKIP_COPILOT_E2E") == "1" {
		t.Skip("SKIP_COPILOT_E2E is set")
	}

	// The Go SDK spawns copilot with --headless --stdio flags.
	// The native copilot binary doesn't support these — we need to point
	// CLIPath to the JS SDK entry point bundled in @github/copilot-sdk.
	cliPath := findCopilotSDKCLIPath()
	if cliPath == "" {
		t.Skip("copilot SDK CLI path not found — install @github/copilot-sdk globally via npm")
	}
	t.Logf("Using CLI path: %s", cliPath)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// 1. Create and start client
	client := copilot.NewClient(&copilot.ClientOptions{
		CLIPath:  cliPath,
		LogLevel: "error",
	})

	err := client.Start(ctx)
	require.NoError(t, err, "client.Start failed — is copilot CLI installed and authenticated?")
	defer func() {
		stopErr := client.Stop()
		if stopErr != nil {
			t.Logf("client.Stop error: %v", stopErr)
		}
	}()

	t.Logf("Client started, state: %s", client.State())
	require.Equal(t, copilot.StateConnected, client.State())

	// 2. Check auth
	auth, err := client.GetAuthStatus(ctx)
	require.NoError(t, err)
	t.Logf("Auth: authenticated=%v, login=%v", auth.IsAuthenticated, auth.Login)
	require.True(t, auth.IsAuthenticated, "not authenticated with GitHub Copilot")

	// 3. List models
	models, err := client.ListModels(ctx)
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
	})
	require.NoError(t, err, "CreateSession failed")
	t.Logf("Session created: %s", session.WorkspacePath())
	defer func() {
		if destroyErr := session.Destroy(); destroyErr != nil {
			t.Logf("session.Destroy error: %v", destroyErr)
		}
	}()

	// 5. Collect events
	var events []copilot.SessionEvent
	unsubscribe := session.On(func(event copilot.SessionEvent) {
		events = append(events, event)
		t.Logf("Event: type=%s", event.Type)
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
	if response != nil && response.Data.Content != nil {
		t.Logf("Response content: %s", *response.Data.Content)
		require.Contains(t, *response.Data.Content, "4",
			"expected response to contain '4'")
	} else {
		// If SendAndWait returned nil, check events for assistant message
		var found bool
		for _, e := range events {
			if e.Type == copilot.AssistantMessage && e.Data.Content != nil {
				t.Logf("Found assistant message in events: %s", *e.Data.Content)
				found = true
				break
			}
		}
		if !found {
			// Log all event types for debugging
			for _, e := range events {
				detail := ""
				if e.Data.Content != nil {
					detail = fmt.Sprintf(" content=%s", truncateForLog(*e.Data.Content, 100))
				}
				t.Logf("  event: type=%s%s", e.Type, detail)
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

// findCopilotSDKCLIPath locates the native Copilot CLI binary bundled in the
// @github/copilot-sdk npm package. This binary supports --headless --stdio
// required by the Go SDK, unlike the copilot shim installed in PATH.
func findCopilotSDKCLIPath() string {
	if p := os.Getenv("COPILOT_CLI_PATH"); p != "" {
		return p
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	// Map Go arch to npm platform arch naming
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x64"
	case "386":
		arch = "ia32"
	}

	// Platform-specific binary package name
	var platformPkg string
	switch runtime.GOOS {
	case "windows":
		platformPkg = fmt.Sprintf("copilot-win32-%s", arch)
	case "darwin":
		platformPkg = fmt.Sprintf("copilot-darwin-%s", arch)
	case "linux":
		platformPkg = fmt.Sprintf("copilot-linux-%s", arch)
	default:
		return ""
	}

	binaryName := "copilot"
	if runtime.GOOS == "windows" {
		binaryName = "copilot.exe"
	}

	// Search common npm global node_modules locations
	candidates := []string{
		filepath.Join(home, "AppData", "Roaming", "npm", "node_modules"),
		filepath.Join(home, ".npm-global", "lib", "node_modules"),
		"/usr/local/lib/node_modules",
		"/usr/lib/node_modules",
	}

	for _, c := range candidates {
		p := filepath.Join(c, "@github", "copilot-sdk", "node_modules", "@github", platformPkg, binaryName)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return ""
}

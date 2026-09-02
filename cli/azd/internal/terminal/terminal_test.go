// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package terminal

import (
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/runcontext/agentdetect"
	"github.com/stretchr/testify/assert"
)

func TestIsTerminal_ForceTTY(t *testing.T) {
	clearTestEnvVars(t)
	agentdetect.ResetDetection()

	// Test AZD_FORCE_TTY=true forces TTY mode
	t.Setenv("AZD_FORCE_TTY", "true")
	assert.True(t, IsTerminal(0, 0), "AZD_FORCE_TTY=true should force TTY mode")

	// Test AZD_FORCE_TTY=false forces non-TTY mode
	t.Setenv("AZD_FORCE_TTY", "false")
	assert.False(t, IsTerminal(0, 0), "AZD_FORCE_TTY=false should disable TTY mode")
}

func TestIsTerminal_AgentDetection(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		expected bool
	}{
		{
			name:     "Claude Code agent disables TTY",
			envVars:  map[string]string{"CLAUDE_CODE": "1"},
			expected: false,
		},
		{
			name:     "GitHub Copilot CLI disables TTY",
			envVars:  map[string]string{"GITHUB_COPILOT_CLI": "true"},
			expected: false,
		},
		{
			name:     "Codex disables TTY",
			envVars:  map[string]string{"CODEX_THREAD_ID": "thread-id"},
			expected: false,
		},
		{
			name:     "Cursor disables TTY",
			envVars:  map[string]string{"CURSOR_AGENT": "1"},
			expected: false,
		},
		{
			name:     "GitHub Copilot CLI via COPILOT_CLI disables TTY",
			envVars:  map[string]string{"COPILOT_CLI": "true"},
			expected: false,
		},
		{
			name:     "GitHub Copilot VSCode disables TTY",
			envVars:  map[string]string{"AI_AGENT": "github_copilot_vscode_agent"},
			expected: false,
		},
		{
			name:     "Gemini CLI disables TTY",
			envVars:  map[string]string{"GEMINI_CLI": "1"},
			expected: false,
		},
		{
			name:     "OpenCode disables TTY",
			envVars:  map[string]string{"OPENCODE": "1"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearTestEnvVars(t)
			agentdetect.ResetDetection()

			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			result := IsTerminal(0, 0)
			assert.Equal(t, tt.expected, result,
				"IsTerminal should return %v when agent is detected", tt.expected)
		})
	}
}

func TestIsTerminal_ForceTTYOverridesAgent(t *testing.T) {
	clearTestEnvVars(t)
	agentdetect.ResetDetection()

	// Set an agent env var that would normally disable TTY
	t.Setenv("CLAUDE_CODE", "1")

	// But AZD_FORCE_TTY should take precedence
	t.Setenv("AZD_FORCE_TTY", "true")

	assert.True(t, IsTerminal(0, 0),
		"AZD_FORCE_TTY=true should override agent detection and enable TTY")
}

func TestIsTerminal_ForceTTYOverridesAntigravity(t *testing.T) {
	clearTestEnvVars(t)
	agentdetect.ResetDetection()
	t.Setenv("ANTIGRAVITY_AGENT", "1")

	t.Setenv("AZD_FORCE_TTY", "false")
	assert.False(t, IsTerminal(0, 0), "AZD_FORCE_TTY=false should disable TTY for Antigravity")

	t.Setenv("AZD_FORCE_TTY", "true")
	assert.True(t, IsTerminal(0, 0), "AZD_FORCE_TTY=true should enable TTY for Antigravity")
}

// clearTestEnvVars clears environment variables that affect terminal detection.
func clearTestEnvVars(t *testing.T) {
	envVarsToUnset := []string{
		"AZD_FORCE_TTY",
		// Agent env vars
		"ANTIGRAVITY_AGENT", "ANTIGRAVITY_CONVERSATION_ID",
		"AI_AGENT", // GitHub Copilot hosts
		"CLAUDE_CODE", "CLAUDE_CODE_ENTRYPOINT",
		"CODEX_CI", "CODEX_THREAD_ID", "CODEX_SESSION_ID",
		"CURSOR_AGENT", "CURSOR_CONVERSATION_ID",
		"GITHUB_COPILOT_CLI", "GH_COPILOT", "COPILOT_CLI",
		"GEMINI_CLI", "GEMINI_CLI_NO_RELAUNCH",
		"OPENCODE",
		// CI env vars
		"CI", "TF_BUILD", "GITHUB_ACTIONS",
	}

	for _, envVar := range envVarsToUnset {
		if _, exists := os.LookupEnv(envVar); exists {
			t.Setenv(envVar, "")
			os.Unsetenv(envVar)
		}
	}
}

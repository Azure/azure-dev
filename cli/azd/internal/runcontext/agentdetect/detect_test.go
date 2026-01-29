// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agentdetect

import (
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentType_DisplayName(t *testing.T) {
	tests := []struct {
		agentType   AgentType
		displayName string
	}{
		{AgentTypeClaudeCode, "Claude Code"},
		{AgentTypeGitHubCopilotCLI, "GitHub Copilot CLI"},
		{AgentTypeOpenAICodex, "OpenAI Codex"},
		{AgentTypeCursor, "Cursor"},
		{AgentTypeWindsurf, "Windsurf"},
		{AgentTypeAider, "Aider"},
		{AgentTypeContinue, "Continue"},
		{AgentTypeAmazonQ, "Amazon Q Developer"},
		{AgentTypeVSCodeCopilot, "VS Code GitHub Copilot"},
		{AgentTypeCline, "Cline"},
		{AgentTypeZed, "Zed"},
		{AgentTypeTabnine, "Tabnine"},
		{AgentTypeCody, "Cody"},
		{AgentTypeGeneric, "Generic Agent"},
		{AgentTypeUnknown, "Unknown"},
	}

	for _, tt := range tests {
		t.Run(string(tt.agentType), func(t *testing.T) {
			assert.Equal(t, tt.displayName, tt.agentType.DisplayName())
		})
	}
}

func TestNoAgent(t *testing.T) {
	agent := NoAgent()
	assert.False(t, agent.Detected)
	assert.Equal(t, AgentTypeUnknown, agent.Type)
	assert.Empty(t, agent.Name)
	assert.Equal(t, DetectionSourceNone, agent.Source)
}

func TestDetectFromEnvVars(t *testing.T) {
	tests := []struct {
		name          string
		envVars       map[string]string
		expectedAgent AgentType
		detected      bool
	}{
		{
			name:          "No env vars",
			envVars:       map[string]string{},
			expectedAgent: AgentTypeUnknown,
			detected:      false,
		},
		{
			name:          "Claude Code via CLAUDE_CODE",
			envVars:       map[string]string{"CLAUDE_CODE": "1"},
			expectedAgent: AgentTypeClaudeCode,
			detected:      true,
		},
		{
			name:          "Claude Code via CLAUDE_CODE_ENTRYPOINT",
			envVars:       map[string]string{"CLAUDE_CODE_ENTRYPOINT": "/usr/bin/claude"},
			expectedAgent: AgentTypeClaudeCode,
			detected:      true,
		},
		{
			name:          "GitHub Copilot CLI",
			envVars:       map[string]string{"GITHUB_COPILOT_CLI": "true"},
			expectedAgent: AgentTypeGitHubCopilotCLI,
			detected:      true,
		},
		{
			name:          "Cursor",
			envVars:       map[string]string{"CURSOR_EDITOR": "1"},
			expectedAgent: AgentTypeCursor,
			detected:      true,
		},
		{
			name:          "Windsurf",
			envVars:       map[string]string{"WINDSURF_EDITOR": "true"},
			expectedAgent: AgentTypeWindsurf,
			detected:      true,
		},
		{
			name:          "Aider",
			envVars:       map[string]string{"AIDER_MODEL": "gpt-4"},
			expectedAgent: AgentTypeAider,
			detected:      true,
		},
		{
			name:          "Amazon Q",
			envVars:       map[string]string{"AMAZON_Q_DEVELOPER": "1"},
			expectedAgent: AgentTypeAmazonQ,
			detected:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear any existing env vars that might interfere
			clearAgentEnvVars(t)

			// Set test env vars
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			result := detectFromEnvVars()

			assert.Equal(t, tt.detected, result.Detected)
			assert.Equal(t, tt.expectedAgent, result.Type)

			if tt.detected {
				assert.Equal(t, DetectionSourceEnvVar, result.Source)
				assert.NotEmpty(t, result.Details)
			}
		})
	}
}

func TestDetectFromUserAgent(t *testing.T) {
	tests := []struct {
		name          string
		userAgent     string
		expectedAgent AgentType
		detected      bool
	}{
		{
			name:          "Empty user agent",
			userAgent:     "",
			expectedAgent: AgentTypeUnknown,
			detected:      false,
		},
		{
			name:          "Unrecognized user agent",
			userAgent:     "some-random-tool/1.0.0",
			expectedAgent: AgentTypeUnknown,
			detected:      false,
		},
		{
			name:          "Claude Code in user agent",
			userAgent:     "claude-code/1.2.3",
			expectedAgent: AgentTypeClaudeCode,
			detected:      true,
		},
		{
			name:          "GitHub Copilot in user agent",
			userAgent:     "github-copilot/2.0.0",
			expectedAgent: AgentTypeGitHubCopilotCLI,
			detected:      true,
		},
		{
			name:          "Cursor in user agent",
			userAgent:     "cursor/0.5.0",
			expectedAgent: AgentTypeCursor,
			detected:      true,
		},
		{
			name:          "VS Code Azure Copilot extension",
			userAgent:     internal.VsCodeAzureCopilotAgentPrefix + "/1.0.0",
			expectedAgent: AgentTypeVSCodeCopilot,
			detected:      true,
		},
		{
			name:          "Case insensitive matching",
			userAgent:     "CLAUDE-CODE/1.0.0",
			expectedAgent: AgentTypeClaudeCode,
			detected:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(internal.AzdUserAgentEnvVar, tt.userAgent)

			result := detectFromUserAgent()

			assert.Equal(t, tt.detected, result.Detected)
			assert.Equal(t, tt.expectedAgent, result.Type)

			if tt.detected {
				assert.Equal(t, DetectionSourceUserAgent, result.Source)
				assert.Equal(t, tt.userAgent, result.Details)
			}
		})
	}
}

func TestMatchProcessToAgent(t *testing.T) {
	tests := []struct {
		name          string
		processInfo   parentProcessInfo
		expectedAgent AgentType
		detected      bool
	}{
		{
			name:          "Empty process info",
			processInfo:   parentProcessInfo{},
			expectedAgent: AgentTypeUnknown,
			detected:      false,
		},
		{
			name: "Claude process name",
			processInfo: parentProcessInfo{
				Name: "claude",
			},
			expectedAgent: AgentTypeClaudeCode,
			detected:      true,
		},
		{
			name: "Claude Code process name",
			processInfo: parentProcessInfo{
				Name: "claude-code",
			},
			expectedAgent: AgentTypeClaudeCode,
			detected:      true,
		},
		{
			name: "Cursor executable on Windows",
			processInfo: parentProcessInfo{
				Name:       "Cursor.exe",
				Executable: "C:\\Users\\test\\AppData\\Local\\Programs\\Cursor\\Cursor.exe",
			},
			expectedAgent: AgentTypeCursor,
			detected:      true,
		},
		{
			name: "Cursor executable on macOS",
			processInfo: parentProcessInfo{
				Name:       "Cursor",
				Executable: "/Applications/Cursor.app/Contents/MacOS/Cursor",
			},
			expectedAgent: AgentTypeCursor,
			detected:      true,
		},
		{
			name: "GitHub Copilot CLI",
			processInfo: parentProcessInfo{
				Name: "gh-copilot",
			},
			expectedAgent: AgentTypeGitHubCopilotCLI,
			detected:      true,
		},
		{
			name: "Windsurf",
			processInfo: parentProcessInfo{
				Name: "windsurf",
			},
			expectedAgent: AgentTypeWindsurf,
			detected:      true,
		},
		{
			name: "Aider",
			processInfo: parentProcessInfo{
				Name: "aider",
			},
			expectedAgent: AgentTypeAider,
			detected:      true,
		},
		{
			name: "Unknown process",
			processInfo: parentProcessInfo{
				Name:       "bash",
				Executable: "/bin/bash",
			},
			expectedAgent: AgentTypeUnknown,
			detected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchProcessToAgent(tt.processInfo)

			assert.Equal(t, tt.detected, result.Detected)
			assert.Equal(t, tt.expectedAgent, result.Type)

			if tt.detected {
				assert.Equal(t, DetectionSourceParentProcess, result.Source)
			}
		})
	}
}

func TestGetCallingAgent_Caching(t *testing.T) {
	clearAgentEnvVars(t)
	ResetDetection()

	// First call - no agent
	agent1 := GetCallingAgent()
	require.False(t, agent1.Detected)

	// Set an env var - but cached result should be returned
	t.Setenv("CLAUDE_CODE", "1")
	agent2 := GetCallingAgent()
	assert.False(t, agent2.Detected, "Should return cached result")

	// Reset and try again
	ResetDetection()
	agent3 := GetCallingAgent()
	assert.True(t, agent3.Detected, "Should detect after reset")
	assert.Equal(t, AgentTypeClaudeCode, agent3.Type)
}

func TestIsRunningInAgent(t *testing.T) {
	clearAgentEnvVars(t)
	ResetDetection()

	assert.False(t, IsRunningInAgent())

	t.Setenv("CURSOR_EDITOR", "1")
	ResetDetection()

	assert.True(t, IsRunningInAgent())
}

// clearAgentEnvVars clears all environment variables that could trigger agent detection.
// This list must be kept in sync with knownEnvVarPatterns in detect_env.go.
func clearAgentEnvVars(t *testing.T) {
	envVarsToUnset := []string{
		// Claude Code
		"CLAUDE_CODE", "CLAUDE_CODE_ENTRYPOINT",
		// GitHub Copilot CLI
		"GITHUB_COPILOT_CLI", "GH_COPILOT",
		// OpenAI Codex
		"OPENAI_CODEX", "CODEX_CLI",
		// Cursor
		"CURSOR_EDITOR", "CURSOR_SESSION_ID", "CURSOR_TRACE_ID",
		// Windsurf
		"WINDSURF_EDITOR", "WINDSURF_SESSION",
		// Zed
		"ZED_TERM",
		// Aider
		"AIDER_MODEL", "AIDER_CHAT_LANGUAGE",
		// Continue
		"CONTINUE_GLOBAL_DIR", "CONTINUE_DEVELOPMENT",
		// Amazon Q
		"AMAZON_Q_DEVELOPER", "AWS_Q_DEVELOPER", "KIRO_CLI",
		// Cline
		"CLINE_MCP",
		// Tabnine
		"TABNINE_CONFIG",
		// Cody
		"CODY_CONFIG",
		// Gemini CLI
		"GEMINI_CLI", "GEMINI_CLI_NO_RELAUNCH", "GEMINI_CODE_ASSIST",
		// OpenCode
		"OPENCODE",
		// User agent
		internal.AzdUserAgentEnvVar,
	}

	for _, envVar := range envVarsToUnset {
		if _, exists := os.LookupEnv(envVar); exists {
			t.Setenv(envVar, "")
			os.Unsetenv(envVar)
		}
	}
}

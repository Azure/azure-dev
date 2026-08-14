// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agentdetect

import (
	"os"
	"strings"
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
		{AgentTypeGitHubCopilotApp, "GitHub Copilot App"},
		{AgentTypeGitHubCopilotVSCode, "GitHub Copilot VSCode"},
		{AgentTypeVSCodeCopilot, "VS Code GitHub Copilot"},
		{AgentTypeGemini, "Gemini"},
		{AgentTypeOpenCode, "OpenCode"},
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
			name: "GitHub Copilot App takes precedence over Copilot CLI",
			envVars: map[string]string{
				"AI_AGENT":    "github_copilot_app_agent",
				"COPILOT_CLI": "1",
			},
			expectedAgent: AgentTypeGitHubCopilotApp,
			detected:      true,
		},
		{
			name: "GitHub Copilot VSCode takes precedence over Copilot CLI",
			envVars: map[string]string{
				"AI_AGENT":           "github_copilot_vscode_agent",
				"GITHUB_COPILOT_CLI": "true",
				"GH_COPILOT":         "1",
				"COPILOT_CLI":        "1",
			},
			expectedAgent: AgentTypeGitHubCopilotVSCode,
			detected:      true,
		},
		{
			name:          "Unrecognized AI_AGENT value",
			envVars:       map[string]string{"AI_AGENT": "another_agent"},
			expectedAgent: AgentTypeUnknown,
			detected:      false,
		},
		{
			name: "AI_AGENT requires an exact value",
			envVars: map[string]string{
				"AI_AGENT": "github_copilot_vscode_agent_extra",
			},
			expectedAgent: AgentTypeUnknown,
			detected:      false,
		},
		{
			name: "Wrong AI_AGENT value falls back to Copilot CLI",
			envVars: map[string]string{
				"AI_AGENT":    "github_copilot_vscode",
				"COPILOT_CLI": "1",
			},
			expectedAgent: AgentTypeGitHubCopilotCLI,
			detected:      true,
		},
		{
			name:          "COPILOT_AGENT alone is not attributed",
			envVars:       map[string]string{"COPILOT_AGENT": "1"},
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
			name:          "GitHub Copilot CLI via GITHUB_COPILOT_CLI",
			envVars:       map[string]string{"GITHUB_COPILOT_CLI": "true"},
			expectedAgent: AgentTypeGitHubCopilotCLI,
			detected:      true,
		},
		{
			name:          "GitHub Copilot CLI via GH_COPILOT",
			envVars:       map[string]string{"GH_COPILOT": "1"},
			expectedAgent: AgentTypeGitHubCopilotCLI,
			detected:      true,
		},
		{
			name:          "GitHub Copilot CLI via COPILOT_CLI",
			envVars:       map[string]string{"COPILOT_CLI": "1"},
			expectedAgent: AgentTypeGitHubCopilotCLI,
			detected:      true,
		},
		{
			name:          "Gemini CLI via GEMINI_CLI",
			envVars:       map[string]string{"GEMINI_CLI": "1"},
			expectedAgent: AgentTypeGemini,
			detected:      true,
		},
		{
			name:          "Gemini CLI via GEMINI_CLI_NO_RELAUNCH",
			envVars:       map[string]string{"GEMINI_CLI_NO_RELAUNCH": "1"},
			expectedAgent: AgentTypeGemini,
			detected:      true,
		},
		{
			name:          "OpenCode via OPENCODE",
			envVars:       map[string]string{"OPENCODE": "1"},
			expectedAgent: AgentTypeOpenCode,
			detected:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearAgentEnvVars(t)

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
			name:          "VS Code Azure Copilot extension",
			userAgent:     internal.VsCodeAzureCopilotAgentPrefix + "/1.0.0",
			expectedAgent: AgentTypeVSCodeCopilot,
			detected:      true,
		},
		{
			name:          "Gemini in user agent",
			userAgent:     "gemini-cli/1.0.0",
			expectedAgent: AgentTypeGemini,
			detected:      true,
		},
		{
			name:          "OpenCode in user agent",
			userAgent:     "opencode/0.1.0",
			expectedAgent: AgentTypeOpenCode,
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
	}{
		{
			name:          "Empty process info",
			processInfo:   parentProcessInfo{},
			expectedAgent: AgentTypeUnknown,
		},
		{
			name: "Claude process name",
			processInfo: parentProcessInfo{
				Name: "claude",
			},
			expectedAgent: AgentTypeClaudeCode,
		},
		{
			name: "Claude Code process name",
			processInfo: parentProcessInfo{
				Name: "claude-code",
			},
			expectedAgent: AgentTypeClaudeCode,
		},
		{
			name: "GitHub Copilot CLI process name",
			processInfo: parentProcessInfo{
				Name: "gh-copilot",
			},
			expectedAgent: AgentTypeGitHubCopilotCLI,
		},
		{
			name: "GitHub Copilot CLI Windows executable",
			processInfo: parentProcessInfo{
				Executable: `C:\Users\example\AppData\Local\Programs\GitHub CLI\gh-copilot.exe`,
			},
			expectedAgent: AgentTypeGitHubCopilotCLI,
		},
		{
			name: "GitHub Copilot CLI executable variants",
			processInfo: parentProcessInfo{
				Executable: "/usr/local/bin/github-copilot-cli",
			},
			expectedAgent: AgentTypeGitHubCopilotCLI,
		},
		{
			name: "Gemini process",
			processInfo: parentProcessInfo{
				Name: "gemini",
			},
			expectedAgent: AgentTypeGemini,
		},
		{
			name: "OpenCode process",
			processInfo: parentProcessInfo{
				Name: "opencode",
			},
			expectedAgent: AgentTypeOpenCode,
		},
		{
			name: "Unknown process",
			processInfo: parentProcessInfo{
				Name:       "bash",
				Executable: "/bin/bash",
			},
			expectedAgent: AgentTypeUnknown,
		},
		{
			name: "GitHub Copilot desktop app is not Copilot CLI",
			processInfo: parentProcessInfo{
				Name:       "GitHub Copilot.exe",
				Executable: `C:\Users\example\AppData\Local\GitHub Copilot\GitHub Copilot.exe`,
			},
			expectedAgent: AgentTypeUnknown,
		},
		{
			name: "Copilot name in host path does not match",
			processInfo: parentProcessInfo{
				Name:       "pwsh.exe",
				Executable: `C:\Users\example\copilot-workspace\pwsh.exe`,
			},
			expectedAgent: AgentTypeUnknown,
		},
		{
			name: "Copilot name substring does not match",
			processInfo: parentProcessInfo{
				Name: "my-copilot-wrapper",
			},
			expectedAgent: AgentTypeUnknown,
		},
		{
			name: "Claude wrapper remains supported",
			processInfo: parentProcessInfo{
				Name: "my-claude-wrapper",
			},
			expectedAgent: AgentTypeClaudeCode,
		},
		{
			name: "Gemini installation path remains supported",
			processInfo: parentProcessInfo{
				Name:       "node",
				Executable: "/usr/local/lib/google-gemini/bin/node",
			},
			expectedAgent: AgentTypeGemini,
		},
		{
			name: "OpenCode versioned executable remains supported",
			processInfo: parentProcessInfo{
				Name: "opencode-v1.2.3",
			},
			expectedAgent: AgentTypeOpenCode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchProcessToAgent(tt.processInfo)

			assert.Equal(t, tt.expectedAgent != AgentTypeUnknown, result.Detected)
			assert.Equal(t, tt.expectedAgent, result.Type)

			if result.Detected {
				assert.Equal(t, DetectionSourceParentProcess, result.Source)
			}
		})
	}
}

func TestMatchProcessToAgent_ExactExecutableNames(t *testing.T) {
	tests := []struct {
		processName   string
		expectedAgent AgentType
	}{
		{"claude", AgentTypeClaudeCode},
		{"claude-code", AgentTypeClaudeCode},
		{"copilot", AgentTypeGitHubCopilotCLI},
		{"copilot-cli", AgentTypeGitHubCopilotCLI},
		{"gh-copilot", AgentTypeGitHubCopilotCLI},
		{"github-copilot", AgentTypeGitHubCopilotCLI},
		{"github-copilot-cli", AgentTypeGitHubCopilotCLI},
		{"gemini", AgentTypeGemini},
		{"gemini-code", AgentTypeGemini},
		{"google-gemini", AgentTypeGemini},
		{"opencode", AgentTypeOpenCode},
	}

	for _, tt := range tests {
		t.Run(tt.processName, func(t *testing.T) {
			result := matchProcessToAgent(parentProcessInfo{
				Name:       strings.ToUpper(tt.processName) + ".EXE",
				Executable: `C:\tools\` + tt.processName + ".exe",
			})

			assert.True(t, result.Detected)
			assert.Equal(t, tt.expectedAgent, result.Type)
			assert.Equal(t, DetectionSourceParentProcess, result.Source)
		})
	}
}

func TestGetCallingAgent_Caching(t *testing.T) {
	clearAgentEnvVars(t)
	ResetDetection()
	skipIfProcessDetectsAgent(t)

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
	skipIfProcessDetectsAgent(t)

	assert.False(t, IsRunningInAgent())

	t.Setenv("GEMINI_CLI", "1")
	ResetDetection()

	assert.True(t, IsRunningInAgent())
}

func TestDisableAgentDetect(t *testing.T) {
	// Even when an agent env var is set, detection should be disabled
	// when AZD_DISABLE_AGENT_DETECT is set.
	t.Setenv("CLAUDE_CODE", "1")
	t.Setenv(DisableAgentDetectEnvVar, "1")
	ResetDetection()

	assert.False(t, IsRunningInAgent(), "Agent detection should be suppressed by %s", DisableAgentDetectEnvVar)

	agent := GetCallingAgent()
	assert.False(t, agent.Detected)
}

// clearAgentEnvVars clears all environment variables that could trigger agent detection.
// This list must be kept in sync with knownEnvVarPatterns in detect_env.go.
func clearAgentEnvVars(t *testing.T) {
	envVarsToUnset := []string{
		// GitHub Copilot hosts
		"AI_AGENT",
		// Claude Code
		"CLAUDE_CODE", "CLAUDE_CODE_ENTRYPOINT",
		// GitHub Copilot CLI
		"GITHUB_COPILOT_CLI", "GH_COPILOT", "COPILOT_CLI",
		// Gemini CLI
		"GEMINI_CLI", "GEMINI_CLI_NO_RELAUNCH",
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

// skipIfProcessDetectsAgent skips the test when parent-process detection finds an agent
// (e.g. running inside Copilot CLI, Claude Code, etc.). Tests that assert "no agent
// detected" cannot pass when the process tree itself contains an agent.
func skipIfProcessDetectsAgent(t *testing.T) {
	t.Helper()
	if GetCallingAgent().Detected {
		t.Skip("skipping: parent process detection found an agent (test is running inside an agent)")
	}
	ResetDetection()
}

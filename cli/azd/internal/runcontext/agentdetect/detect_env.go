// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agentdetect

import (
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
)

// envVarPattern maps environment variables to agent types.
type envVarPattern struct {
	envVar    string
	agentType AgentType
}

// knownEnvVarPatterns defines environment variables that indicate known AI agents.
// These are checked in order, so more specific patterns should come first.
var knownEnvVarPatterns = []envVarPattern{
	// Claude Code - Anthropic's coding agent
	{envVar: "CLAUDE_CODE", agentType: AgentTypeClaudeCode},
	{envVar: "CLAUDE_CODE_ENTRYPOINT", agentType: AgentTypeClaudeCode},

	// GitHub Copilot CLI
	{envVar: "GITHUB_COPILOT_CLI", agentType: AgentTypeGitHubCopilotCLI},
	{envVar: "GH_COPILOT", agentType: AgentTypeGitHubCopilotCLI},

	// Google Gemini CLI
	{envVar: "GEMINI_CLI", agentType: AgentTypeGemini},
	{envVar: "GEMINI_CLI_NO_RELAUNCH", agentType: AgentTypeGemini},

	// OpenCode - AI coding CLI
	{envVar: "OPENCODE", agentType: AgentTypeOpenCode},
}

// detectFromEnvVars checks for known AI agent environment variables.
func detectFromEnvVars() AgentInfo {
	for _, pattern := range knownEnvVarPatterns {
		if _, exists := os.LookupEnv(pattern.envVar); exists {
			return AgentInfo{
				Type:     pattern.agentType,
				Name:     pattern.agentType.DisplayName(),
				Source:   DetectionSourceEnvVar,
				Detected: true,
				Details:  pattern.envVar,
			}
		}
	}

	return NoAgent()
}

// userAgentPatterns maps user agent substrings to agent types.
// Matched case-insensitively against AZURE_DEV_USER_AGENT.
var userAgentPatterns = []struct {
	substring string
	agentType AgentType
}{
	// VS Code GitHub Copilot extension
	{substring: internal.VsCodeAzureCopilotAgentPrefix, agentType: AgentTypeVSCodeCopilot},
	{substring: "github-copilot", agentType: AgentTypeGitHubCopilotCLI},
	{substring: "copilot-cli", agentType: AgentTypeGitHubCopilotCLI},
	{substring: "claude-code", agentType: AgentTypeClaudeCode},
	{substring: "claude", agentType: AgentTypeClaudeCode},
	{substring: "gemini", agentType: AgentTypeGemini},
	{substring: "opencode", agentType: AgentTypeOpenCode},
}

// detectFromUserAgent checks the AZURE_DEV_USER_AGENT env var for known agents.
func detectFromUserAgent() AgentInfo {
	userAgent := os.Getenv(internal.AzdUserAgentEnvVar)
	if userAgent == "" {
		return NoAgent()
	}

	userAgentLower := strings.ToLower(userAgent)

	for _, pattern := range userAgentPatterns {
		if strings.Contains(userAgentLower, strings.ToLower(pattern.substring)) {
			return AgentInfo{
				Type:     pattern.agentType,
				Name:     pattern.agentType.DisplayName(),
				Source:   DetectionSourceUserAgent,
				Detected: true,
				Details:  userAgent,
			}
		}
	}

	return NoAgent()
}

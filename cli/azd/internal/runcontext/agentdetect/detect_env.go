// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agentdetect

import (
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
)

// agentEnvVarPatterns maps environment variables to agent types.
// Each entry defines env vars that indicate a specific agent is running.
type envVarPattern struct {
	envVar    string
	agentType AgentType
	// checkValue optionally validates the env var value (if empty, presence is enough)
	checkValue string
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

	// OpenAI Codex CLI
	{envVar: "OPENAI_CODEX", agentType: AgentTypeOpenAICodex},
	{envVar: "CODEX_CLI", agentType: AgentTypeOpenAICodex},

	// Cursor editor - VS Code fork with AI
	{envVar: "CURSOR_EDITOR", agentType: AgentTypeCursor},
	{envVar: "CURSOR_SESSION_ID", agentType: AgentTypeCursor},
	{envVar: "CURSOR_TRACE_ID", agentType: AgentTypeCursor},

	// Windsurf editor (by Codeium) - VS Code fork
	{envVar: "WINDSURF_EDITOR", agentType: AgentTypeWindsurf},
	{envVar: "WINDSURF_SESSION", agentType: AgentTypeWindsurf},

	// Zed editor - Rust-based editor with AI
	{envVar: "ZED_TERM", agentType: AgentTypeZed},

	// Aider - AI pair programming tool
	{envVar: "AIDER_MODEL", agentType: AgentTypeAider},
	{envVar: "AIDER_CHAT_LANGUAGE", agentType: AgentTypeAider},
	{envVar: "AIDER_OPENAI_API_KEY", agentType: AgentTypeAider},

	// Continue coding assistant
	{envVar: "CONTINUE_GLOBAL_DIR", agentType: AgentTypeContinue},
	{envVar: "CONTINUE_DEVELOPMENT", agentType: AgentTypeContinue},

	// Amazon Q Developer (formerly CodeWhisperer)
	{envVar: "AMAZON_Q_DEVELOPER", agentType: AgentTypeAmazonQ},
	{envVar: "AWS_Q_DEVELOPER", agentType: AgentTypeAmazonQ},
	{envVar: "CODEWHISPERER_TOKEN", agentType: AgentTypeAmazonQ},
	{envVar: "KIRO_CLI", agentType: AgentTypeAmazonQ},

	// Cline (formerly Claude Dev) - VS Code extension
	{envVar: "CLINE_API_KEY", agentType: AgentTypeCline},
	{envVar: "CLINE_MCP", agentType: AgentTypeCline},

	// Tabnine - AI code completion
	{envVar: "TABNINE_TOKEN", agentType: AgentTypeTabnine},
	{envVar: "TABNINE_CONFIG", agentType: AgentTypeTabnine},

	// Cody (Sourcegraph) - AI coding assistant
	{envVar: "SRC_ACCESS_TOKEN", agentType: AgentTypeCody},
	{envVar: "CODY_CONFIG", agentType: AgentTypeCody},

	// Google Gemini CLI
	{envVar: "GEMINI_CLI", agentType: AgentTypeGemini},
	{envVar: "GEMINI_CLI_NO_RELAUNCH", agentType: AgentTypeGemini},
	{envVar: "GEMINI_API_KEY", agentType: AgentTypeGemini},
	{envVar: "GOOGLE_GEMINI_API_KEY", agentType: AgentTypeGemini},
	{envVar: "GEMINI_CODE_ASSIST", agentType: AgentTypeGemini},
}

// detectFromEnvVars checks for known AI agent environment variables.
func detectFromEnvVars() AgentInfo {
	for _, pattern := range knownEnvVarPatterns {
		if value, exists := os.LookupEnv(pattern.envVar); exists {
			// If checkValue is specified, verify it matches
			if pattern.checkValue != "" && value != pattern.checkValue {
				continue
			}

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
	{substring: "cursor", agentType: AgentTypeCursor},
	{substring: "windsurf", agentType: AgentTypeWindsurf},
	{substring: "aider", agentType: AgentTypeAider},
	{substring: "amazon-q", agentType: AgentTypeAmazonQ},
	{substring: "kiro", agentType: AgentTypeAmazonQ},
	{substring: "cline", agentType: AgentTypeCline},
	{substring: "zed", agentType: AgentTypeZed},
	{substring: "tabnine", agentType: AgentTypeTabnine},
	{substring: "cody", agentType: AgentTypeCody},
	{substring: "sourcegraph", agentType: AgentTypeCody},
	{substring: "gemini", agentType: AgentTypeGemini},
	{substring: "codex", agentType: AgentTypeOpenAICodex},
	{substring: "continue", agentType: AgentTypeContinue},
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

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package agentdetect identifies known AI coding agents in the process environment.
// Callers decide whether inherited agent context should affect behavior.
package agentdetect

// AgentType represents a known AI coding agent.
type AgentType string

const (
	// AgentTypeUnknown indicates no agent was detected.
	AgentTypeUnknown AgentType = ""
	// AgentTypeClaudeCode is Anthropic's Claude Code agent.
	AgentTypeClaudeCode AgentType = "claude-code"
	// AgentTypeClaudeCodeDesktop is Claude Code launched from Claude Desktop.
	AgentTypeClaudeCodeDesktop AgentType = "claude-code-desktop"
	// AgentTypeClaudeCodeVSCode is the Claude Code VS Code integration.
	AgentTypeClaudeCodeVSCode AgentType = "claude-code-vscode"
	// AgentTypeCodex is OpenAI's Codex agent.
	AgentTypeCodex AgentType = "codex"
	// AgentTypeCodexDesktop is the Codex Desktop app.
	AgentTypeCodexDesktop AgentType = "codex-desktop"
	// AgentTypeCursor is Cursor's coding agent.
	AgentTypeCursor AgentType = "cursor"
	// AgentTypeGitHubCopilotCLI is GitHub's Copilot CLI agent.
	AgentTypeGitHubCopilotCLI AgentType = "github-copilot-cli"
	// AgentTypeGitHubCopilotApp is GitHub's Copilot App agent.
	AgentTypeGitHubCopilotApp AgentType = "github-copilot-app"
	// AgentTypeGitHubCopilotVSCode is GitHub Copilot agent mode in VS Code.
	AgentTypeGitHubCopilotVSCode AgentType = "github-copilot-vscode"
	// AgentTypeGitHubCopilotCloudAgent is GitHub Copilot cloud agent.
	AgentTypeGitHubCopilotCloudAgent AgentType = "github-copilot-cloud-agent"
	// AgentTypeVSCodeCopilot is the GitHub Copilot for Azure extension in VS Code.
	AgentTypeVSCodeCopilot AgentType = "vscode-copilot"
	// AgentTypeGemini is Google's Gemini CLI.
	AgentTypeGemini AgentType = "gemini"
	// AgentTypeOpenCode is the OpenCode AI coding CLI.
	AgentTypeOpenCode AgentType = "opencode"
	// AgentTypePi is the Pi coding agent.
	AgentTypePi AgentType = "pi"
)

// String returns the string representation of the agent type.
func (a AgentType) String() string {
	return string(a)
}

// DisplayName returns a human-readable name for the agent type.
func (a AgentType) DisplayName() string {
	switch a {
	case AgentTypeClaudeCode:
		return "Claude Code"
	case AgentTypeClaudeCodeDesktop:
		return "Claude Code Desktop"
	case AgentTypeClaudeCodeVSCode:
		return "Claude Code VSCode"
	case AgentTypeCodex:
		return "Codex CLI"
	case AgentTypeCodexDesktop:
		return "Codex Desktop"
	case AgentTypeCursor:
		return "Cursor"
	case AgentTypeGitHubCopilotCLI:
		return "GitHub Copilot CLI"
	case AgentTypeGitHubCopilotApp:
		return "GitHub Copilot App"
	case AgentTypeGitHubCopilotVSCode:
		return "GitHub Copilot VSCode"
	case AgentTypeGitHubCopilotCloudAgent:
		return "GitHub Copilot Cloud Agent"
	case AgentTypeVSCodeCopilot:
		return "VS Code GitHub Copilot"
	case AgentTypeGemini:
		return "Gemini"
	case AgentTypeOpenCode:
		return "OpenCode"
	case AgentTypePi:
		return "Pi"
	default:
		return "Unknown"
	}
}

// DetectionSource indicates how an agent was detected.
type DetectionSource string

const (
	// DetectionSourceNone indicates no detection occurred.
	DetectionSourceNone DetectionSource = ""
	// DetectionSourceEnvVar indicates detection via environment variable.
	DetectionSourceEnvVar DetectionSource = "env-var"
	// DetectionSourceParentProcess indicates detection via parent process inspection.
	DetectionSourceParentProcess DetectionSource = "parent-process"
	// DetectionSourceUserAgent indicates detection via AZURE_DEV_USER_AGENT.
	DetectionSourceUserAgent DetectionSource = "user-agent"
)

// AgentInfo contains information about a detected AI coding agent.
type AgentInfo struct {
	// Type is the identified agent type.
	Type AgentType
	// Name is a human-readable name for the agent.
	Name string
	// Source indicates how the agent was detected.
	Source DetectionSource
	// Detected is true if an agent was detected.
	Detected bool
	// Details contains additional detection information (e.g., matched env var or process name).
	Details string
}

// NoAgent returns an AgentInfo indicating no agent was detected.
func NoAgent() AgentInfo {
	return AgentInfo{
		Type:     AgentTypeUnknown,
		Name:     "",
		Source:   DetectionSourceNone,
		Detected: false,
	}
}

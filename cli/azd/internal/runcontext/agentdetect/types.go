// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package agentdetect provides functionality to detect when azd is invoked
// by known AI coding agents (Claude Code, GitHub Copilot CLI, Cursor, etc.)
// and enables automatic adjustment of behavior (e.g., no-prompt mode).
package agentdetect

// AgentType represents a known AI coding agent.
type AgentType string

const (
	// AgentTypeUnknown indicates no agent was detected.
	AgentTypeUnknown AgentType = ""
	// AgentTypeClaudeCode is Anthropic's Claude Code agent.
	AgentTypeClaudeCode AgentType = "claude-code"
	// AgentTypeGitHubCopilotCLI is GitHub's Copilot CLI agent.
	AgentTypeGitHubCopilotCLI AgentType = "github-copilot-cli"
	// AgentTypeOpenAICodex is OpenAI's Codex CLI agent.
	AgentTypeOpenAICodex AgentType = "openai-codex"
	// AgentTypeCursor is the Cursor editor agent.
	AgentTypeCursor AgentType = "cursor"
	// AgentTypeWindsurf is the Windsurf editor agent (by Codeium).
	AgentTypeWindsurf AgentType = "windsurf"
	// AgentTypeAider is the Aider AI pair programming tool.
	AgentTypeAider AgentType = "aider"
	// AgentTypeContinue is the Continue coding assistant.
	AgentTypeContinue AgentType = "continue"
	// AgentTypeAmazonQ is Amazon Q Developer agent (formerly CodeWhisperer).
	AgentTypeAmazonQ AgentType = "amazon-q"
	// AgentTypeVSCodeCopilot is VS Code GitHub Copilot extension.
	AgentTypeVSCodeCopilot AgentType = "vscode-copilot"
	// AgentTypeCline is the Cline VS Code extension (formerly Claude Dev).
	AgentTypeCline AgentType = "cline"
	// AgentTypeZed is the Zed editor with AI features.
	AgentTypeZed AgentType = "zed"
	// AgentTypeTabnine is the Tabnine AI coding assistant.
	AgentTypeTabnine AgentType = "tabnine"
	// AgentTypeCody is Sourcegraph's Cody AI assistant.
	AgentTypeCody AgentType = "cody"
	// AgentTypeGemini is Google's Gemini CLI.
	AgentTypeGemini AgentType = "gemini"
	// AgentTypeOpenCode is the OpenCode AI coding CLI.
	AgentTypeOpenCode AgentType = "opencode"
	// AgentTypeGeneric indicates an agent was detected but not specifically identified.
	AgentTypeGeneric AgentType = "generic"
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
	case AgentTypeGitHubCopilotCLI:
		return "GitHub Copilot CLI"
	case AgentTypeOpenAICodex:
		return "OpenAI Codex"
	case AgentTypeCursor:
		return "Cursor"
	case AgentTypeWindsurf:
		return "Windsurf"
	case AgentTypeAider:
		return "Aider"
	case AgentTypeContinue:
		return "Continue"
	case AgentTypeAmazonQ:
		return "Amazon Q Developer"
	case AgentTypeVSCodeCopilot:
		return "VS Code GitHub Copilot"
	case AgentTypeCline:
		return "Cline"
	case AgentTypeZed:
		return "Zed"
	case AgentTypeTabnine:
		return "Tabnine"
	case AgentTypeCody:
		return "Cody"
	case AgentTypeGemini:
		return "Gemini"
	case AgentTypeOpenCode:
		return "OpenCode"
	case AgentTypeGeneric:
		return "Generic Agent"
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

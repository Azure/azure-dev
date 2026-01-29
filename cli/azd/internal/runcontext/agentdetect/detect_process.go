// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agentdetect

import (
	"os"
	"path/filepath"
	"strings"
)

// processNamePatterns maps process name patterns to agent types.
// Patterns are matched case-insensitively against process names and executable paths.
var processNamePatterns = []struct {
	patterns  []string // lowercase patterns to match
	agentType AgentType
}{
	// Claude Code (Anthropic) - installed via npm, homebrew, or direct download
	{
		patterns:  []string{"claude", "claude-code"},
		agentType: AgentTypeClaudeCode,
	},
	// GitHub Copilot CLI - installed via npm (@github/copilot) or as gh extension
	{
		patterns:  []string{"copilot", "copilot-cli", "gh-copilot", "github-copilot", "github-copilot-cli"},
		agentType: AgentTypeGitHubCopilotCLI,
	},
	// OpenAI Codex CLI - Rust-based CLI
	{
		patterns:  []string{"codex", "openai-codex"},
		agentType: AgentTypeOpenAICodex,
	},
	// Cursor Editor - VS Code fork with AI
	{
		patterns:  []string{"cursor"},
		agentType: AgentTypeCursor,
	},
	// Windsurf Editor (by Codeium) - VS Code fork
	{
		patterns:  []string{"windsurf"},
		agentType: AgentTypeWindsurf,
	},
	// Aider - AI pair programming tool (Python-based, may appear as python with aider in args)
	{
		patterns:  []string{"aider", "aider-chat"},
		agentType: AgentTypeAider,
	},
	// Continue - AI coding assistant (CLI is 'cn' command)
	{
		patterns:  []string{"continue", "cn"},
		agentType: AgentTypeContinue,
	},
	// Amazon Q Developer (formerly CodeWhisperer) - CLI is 'q', also 'kiro' for new version
	{
		patterns:  []string{"amazon-q", "q-developer", "chat_cli", "kiro"},
		agentType: AgentTypeAmazonQ,
	},
	// Cline (formerly Claude Dev) - VS Code extension, may have CLI
	{
		patterns:  []string{"cline", "claude-dev"},
		agentType: AgentTypeCline,
	},
	// Zed Editor - Rust-based editor with AI features
	{
		patterns:  []string{"zed"},
		agentType: AgentTypeZed,
	},
	// Tabnine - AI code completion
	{
		patterns:  []string{"tabnine", "tabnine-companion"},
		agentType: AgentTypeTabnine,
	},
	// Cody (Sourcegraph) - AI coding assistant
	{
		patterns:  []string{"cody", "sourcegraph"},
		agentType: AgentTypeCody,
	},
	// Google Gemini CLI
	{
		patterns:  []string{"gemini", "gemini-code", "google-gemini"},
		agentType: AgentTypeGemini,
	},
	// OpenCode - AI coding CLI
	{
		patterns:  []string{"opencode"},
		agentType: AgentTypeOpenCode,
	},
}

// maxProcessTreeDepth limits how far up the process tree we walk to prevent infinite loops.
const maxProcessTreeDepth = 10

// detectFromParentProcess checks if any ancestor process is a known AI agent.
// It walks up the process tree to find agents that spawn intermediate shells.
func detectFromParentProcess() AgentInfo {
	currentPid := os.Getppid()

	for depth := 0; depth < maxProcessTreeDepth && currentPid > 1; depth++ {
		info, parentPid, err := getParentProcessInfoWithPPID(currentPid)
		if err != nil {
			break
		}

		// Check if this process matches a known agent
		agent := matchProcessToAgent(info)
		if agent.Detected {
			return agent
		}

		// Move up the tree
		if parentPid <= 1 || parentPid == currentPid {
			// Reached root or stuck in a loop
			break
		}
		currentPid = parentPid
	}

	return NoAgent()
}

// parentProcessInfo contains information about the parent process.
type parentProcessInfo struct {
	// Name is the process name (e.g., "claude" or "cursor.exe")
	Name string
	// Executable is the full path to the executable (if available)
	Executable string
	// CommandLine is the full command line (if available)
	CommandLine string
}

// matchProcessToAgent matches process info against known agent patterns.
func matchProcessToAgent(info parentProcessInfo) AgentInfo {
	// Normalize for matching
	nameLower := strings.ToLower(info.Name)
	exeLower := strings.ToLower(filepath.Base(info.Executable))

	// Remove common extensions for matching
	nameLower = strings.TrimSuffix(nameLower, ".exe")
	exeLower = strings.TrimSuffix(exeLower, ".exe")

	for _, pattern := range processNamePatterns {
		for _, p := range pattern.patterns {
			if strings.Contains(nameLower, p) || strings.Contains(exeLower, p) {
				matchedOn := info.Name
				if info.Executable != "" {
					matchedOn = info.Executable
				}

				return AgentInfo{
					Type:     pattern.agentType,
					Name:     pattern.agentType.DisplayName(),
					Source:   DetectionSourceParentProcess,
					Detected: true,
					Details:  matchedOn,
				}
			}
		}
	}

	return NoAgent()
}

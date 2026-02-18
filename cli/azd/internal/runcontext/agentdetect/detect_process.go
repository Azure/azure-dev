// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agentdetect

import (
	"log"
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
			log.Printf("detect_process.go: Failed to get process info for pid %d: %v", currentPid, err)
			break
		}

		log.Printf("detect_process.go: Parent process detection: depth=%d, pid=%d, ppid=%d, name=%q, executable=%q",
			depth, currentPid, parentPid, info.Name, info.Executable)

		// Try to match this process against known agents
		result := matchProcessToAgent(info)
		if result.Detected {
			return result
		}

		// Move up to the parent
		if parentPid <= 1 || parentPid == currentPid {
			break
		}
		currentPid = parentPid
	}

	log.Printf("detect_process.go: Parent process detection: no agent found in process tree")
	return NoAgent()
}

// parentProcessInfo contains information about a parent process.
type parentProcessInfo struct {
	Name        string
	Executable  string
	CommandLine string // Full command line (Linux/macOS only)
}

// matchProcessToAgent checks if a process matches any known AI agent patterns.
func matchProcessToAgent(info parentProcessInfo) AgentInfo {
	if info.Name == "" && info.Executable == "" {
		return NoAgent()
	}

	nameLower := strings.ToLower(info.Name)
	execLower := strings.ToLower(info.Executable)
	execBaseLower := strings.ToLower(filepath.Base(info.Executable))

	// Remove common executable extensions for matching
	nameLower = strings.TrimSuffix(nameLower, ".exe")
	execBaseLower = strings.TrimSuffix(execBaseLower, ".exe")

	for _, entry := range processNamePatterns {
		for _, pattern := range entry.patterns {
			// Check against process name
			if nameLower == pattern || strings.Contains(nameLower, pattern) {
				return AgentInfo{
					Type:     entry.agentType,
					Name:     entry.agentType.DisplayName(),
					Source:   DetectionSourceParentProcess,
					Detected: true,
					Details:  info.Name,
				}
			}

			// Check against executable base name
			if execBaseLower == pattern || strings.Contains(execBaseLower, pattern) {
				return AgentInfo{
					Type:     entry.agentType,
					Name:     entry.agentType.DisplayName(),
					Source:   DetectionSourceParentProcess,
					Detected: true,
					Details:  info.Executable,
				}
			}

			// Check if pattern appears in full executable path (for detection via install paths)
			if strings.Contains(execLower, pattern) {
				return AgentInfo{
					Type:     entry.agentType,
					Name:     entry.agentType.DisplayName(),
					Source:   DetectionSourceParentProcess,
					Detected: true,
					Details:  info.Executable,
				}
			}
		}
	}

	return NoAgent()
}

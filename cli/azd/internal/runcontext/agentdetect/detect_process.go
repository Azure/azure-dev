// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agentdetect

import (
	"log"
	"os"
	"strings"
)

// processNamePatterns maps process executable names to agent types.
var processNamePatterns = []struct {
	patterns   []string // lowercase executable names to match
	agentType  AgentType
	exactMatch bool
}{
	// Google Antigravity CLI
	{
		patterns:  []string{"agy"},
		agentType: AgentTypeAntigravity,
		// Avoid classifying unrelated executable paths containing the short "agy" name.
		exactMatch: true,
	},
	// Codex - OpenAI's coding agent
	{
		patterns:  []string{"codex"},
		agentType: AgentTypeCodex,
		// Avoid classifying unrelated executable paths containing "codex".
		exactMatch: true,
	},
	// Claude Code (Anthropic) - installed via npm, homebrew, or direct download
	{
		patterns:  []string{"claude", "claude-code"},
		agentType: AgentTypeClaudeCode,
	},
	// GitHub Copilot CLI - installed via npm (@github/copilot) or as gh extension
	{
		patterns:  []string{"copilot", "copilot-cli", "gh-copilot", "github-copilot", "github-copilot-cli"},
		agentType: AgentTypeGitHubCopilotCLI,
		// Avoid classifying host applications and installation paths containing the generic "copilot" term.
		exactMatch: true,
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

	nameLower := normalizeProcessName(info.Name)
	execBaseLower := normalizeProcessName(info.Executable)

	for _, entry := range processNamePatterns {
		for _, pattern := range entry.patterns {
			if processNameMatches(nameLower, pattern, entry.exactMatch) {
				return AgentInfo{
					Type:     entry.agentType,
					Name:     entry.agentType.DisplayName(),
					Source:   DetectionSourceParentProcess,
					Detected: true,
					Details:  info.Name,
				}
			}

			if processNameMatches(execBaseLower, pattern, entry.exactMatch) ||
				(!entry.exactMatch && strings.Contains(strings.ToLower(info.Executable), pattern)) {
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

func processNameMatches(processName string, pattern string, exactMatch bool) bool {
	if exactMatch {
		return processName == pattern
	}

	return strings.Contains(processName, pattern)
}

func normalizeProcessName(processPath string) string {
	normalizedPath := strings.ReplaceAll(processPath, "\\", "/")
	if index := strings.LastIndex(normalizedPath, "/"); index >= 0 {
		normalizedPath = normalizedPath[index+1:]
	}

	return strings.TrimSuffix(strings.ToLower(normalizedPath), ".exe")
}

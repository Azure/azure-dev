// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"os"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Interactive TUI support
// ---------------------------------------------------------------------------

// InteractiveMode describes the level of interactive support available.
type InteractiveMode string

const (
	// InteractiveFull means the process has a TTY on stdin, stdout, and stderr.
	// Full interactive prompts and TUI elements are available.
	InteractiveFull InteractiveMode = "full"
	// InteractiveLimited means some stdio is a terminal but not all.
	// Limited interaction is possible (e.g., output coloring but no prompts).
	InteractiveLimited InteractiveMode = "limited"
	// InteractiveNone means no stdio is a terminal. The process is running
	// non-interactively (piped, CI, background, cron, etc.).
	InteractiveNone InteractiveMode = "none"
)

// String returns the string representation of the interactive mode.
func (m InteractiveMode) String() string {
	return string(m)
}

// InteractiveInfo describes the interactive capabilities of the current
// process environment.
type InteractiveInfo struct {
	// Mode is the detected interactive mode.
	Mode InteractiveMode
	// StdinTTY is true if stdin is a terminal.
	StdinTTY bool
	// StdoutTTY is true if stdout is a terminal.
	StdoutTTY bool
	// StderrTTY is true if stderr is a terminal.
	StderrTTY bool
	// NoPrompt is true if the AZD_NO_PROMPT environment variable is set,
	// indicating that the user or host has explicitly disabled interactive
	// prompts.
	NoPrompt bool
	// CI is true if a CI environment was detected (CI, GITHUB_ACTIONS,
	// TF_BUILD, JENKINS_URL, etc.).
	CI bool
	// Agent is true if an AI coding agent was detected (CLAUDECODE,
	// COPILOT_CLI, etc.). This identifies the process environment but
	// does not override terminal capabilities.
	Agent bool
}

// CanPrompt reports whether it is safe to show interactive prompts to the
// user. This requires:
//  1. stdin is a TTY
//  2. stdout is a TTY
//  3. AZD_NO_PROMPT is not set
//  4. Not in CI
func (i InteractiveInfo) CanPrompt() bool {
	return i.StdinTTY && i.StdoutTTY && !i.NoPrompt && !i.CI
}

// CanColorize reports whether it is safe to use ANSI color/style codes in
// output. This requires stdout to be a TTY (unless FORCE_COLOR is set).
func (i InteractiveInfo) CanColorize() bool {
	if v, ok := os.LookupEnv("FORCE_COLOR"); ok && v == "1" {
		return true
	}
	if v, ok := os.LookupEnv("NO_COLOR"); ok && v != "" {
		return false
	}
	return i.StdoutTTY
}

// DetectInteractive inspects the current process environment to determine
// interactive capabilities.
//
// Detection checks:
//   - os.Stdin / os.Stdout / os.Stderr file mode (character device = TTY)
//   - AZD_NO_PROMPT environment variable
//   - CI environment variables: CI, GITHUB_ACTIONS, TF_BUILD, JENKINS_URL,
//     GITLAB_CI, CIRCLECI, TRAVIS, BUILDKITE, CODEBUILD_BUILD_ID
//   - Agent environment variables: CLAUDECODE, COPILOT_CLI, and other known
//     agent markers
//
// Platform behavior:
//   - All platforms: Uses os.File.Stat() ModeCharDevice to detect TTY.
//   - Windows: Windows Terminal, ConPTY, and mintty are detected as TTY.
//   - Unix: Standard isatty behavior via file mode.
func DetectInteractive() InteractiveInfo {
	info := InteractiveInfo{
		StdinTTY:  IsInteractiveTerminal(os.Stdin),
		StdoutTTY: IsInteractiveTerminal(os.Stdout),
		StderrTTY: IsInteractiveTerminal(os.Stderr),
		NoPrompt:  isNoPromptEnv(),
		CI:        isCIEnv(),
		Agent:     detectAgentEnv(),
	}

	switch {
	case info.StdinTTY && info.StdoutTTY && info.StderrTTY:
		info.Mode = InteractiveFull
	case info.StdinTTY || info.StdoutTTY || info.StderrTTY:
		info.Mode = InteractiveLimited
	default:
		info.Mode = InteractiveNone
	}

	return info
}

// ---------------------------------------------------------------------------
// Internal detection helpers
// ---------------------------------------------------------------------------

// isNoPromptEnv checks AZD_NO_PROMPT for a truthy value.
func isNoPromptEnv() bool {
	v := os.Getenv("AZD_NO_PROMPT")
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		// Also accept "yes" for backward compatibility.
		return strings.EqualFold(v, "yes")
	}
	return b
}

// ciEnvVars lists environment variables that indicate CI environments.
var ciEnvVars = []string{
	"CI",
	"GITHUB_ACTIONS",
	"TF_BUILD",
	"JENKINS_URL",
	"GITLAB_CI",
	"CIRCLECI",
	"TRAVIS",
	"BUILDKITE",
	"CODEBUILD_BUILD_ID",
}

// isCIEnv reports whether the process is running in a CI environment.
func isCIEnv() bool {
	for _, key := range ciEnvVars {
		if v := os.Getenv(key); v != "" {
			return true
		}
	}
	return false
}

type agentEnvPattern struct {
	envVar        string
	expectedValue string
}

// agentEnvPatterns lists environment variables that indicate AI coding agents.
var agentEnvPatterns = []agentEnvPattern{
	{envVar: "AI_AGENT", expectedValue: "github_copilot_app_agent"},
	{envVar: "AI_AGENT", expectedValue: "github_copilot_vscode_agent"},
	{envVar: "AI_AGENT", expectedValue: "github_copilot_cloud_agent"},
	{envVar: "AI_AGENT", expectedValue: "pi"},
	{envVar: "CODEX_INTERNAL_ORIGINATOR_OVERRIDE", expectedValue: "Codex Desktop"},
	{envVar: "CODEX_CI", expectedValue: "1"},
	{envVar: "CODEX_THREAD_ID"},
	{envVar: "CODEX_SESSION_ID"},
	{envVar: "CURSOR_AGENT", expectedValue: "1"},
	{envVar: "CURSOR_CONVERSATION_ID"},
	{envVar: "CLAUDECODE", expectedValue: "1"},
	{envVar: "COPILOT_CLI"},
	{envVar: "GEMINI_CLI"},
	{envVar: "GEMINI_CLI_NO_RELAUNCH"},
	{envVar: "OPENCODE"},
}

// detectAgentEnv reports whether an AI coding agent was detected.
func detectAgentEnv() bool {
	for _, pattern := range agentEnvPatterns {
		value, exists := os.LookupEnv(pattern.envVar)
		if !exists || value == "" ||
			(pattern.expectedValue != "" && value != pattern.expectedValue) {
			continue
		}

		return true
	}

	return false
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"os"
	"testing"
)

// ---------------------------------------------------------------------------
// InteractiveMode.String
// ---------------------------------------------------------------------------

func TestInteractiveMode_String(t *testing.T) {
	tests := []struct {
		mode InteractiveMode
		want string
	}{
		{InteractiveFull, "full"},
		{InteractiveLimited, "limited"},
		{InteractiveNone, "none"},
	}
	for _, tc := range tests {
		if got := tc.mode.String(); got != tc.want {
			t.Errorf("InteractiveMode(%q).String() = %q, want %q", string(tc.mode), got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// DetectInteractive
// ---------------------------------------------------------------------------

func TestDetectInteractive_NoPanic(t *testing.T) {
	// DetectInteractive should never panic regardless of environment.
	info := DetectInteractive()
	_ = info.Mode
	_ = info.CanPrompt()
	_ = info.CanColorize()
}

func TestDetectInteractive_CIDetection(t *testing.T) {
	// Set CI env and verify detection.
	t.Setenv("CI", "true")

	info := DetectInteractive()
	if !info.CI {
		t.Error("DetectInteractive().CI = false with CI=true, want true")
	}
	// In CI, prompting should be disabled regardless of TTY.
	if info.CanPrompt() {
		t.Error("CanPrompt() = true in CI, want false")
	}
}

func TestDetectInteractive_NoPromptEnv(t *testing.T) {
	// Ensure CI doesn't interfere.
	clearCIEnv(t)

	t.Setenv("AZD_NO_PROMPT", "1")
	info := DetectInteractive()
	if !info.NoPrompt {
		t.Error("DetectInteractive().NoPrompt = false with AZD_NO_PROMPT=1, want true")
	}
}

func TestDetectInteractive_AgentDetection(t *testing.T) {
	clearCIEnv(t)

	t.Setenv("CLAUDE_CODE", "1")
	info := DetectInteractive()
	if !info.Agent {
		t.Error("DetectInteractive().Agent = false with CLAUDE_CODE=1, want true")
	}
	if info.CanPrompt() {
		t.Error("CanPrompt() = true with agent detected, want false")
	}
}

// ---------------------------------------------------------------------------
// CanPrompt
// ---------------------------------------------------------------------------

func TestCanPrompt_AllConditions(t *testing.T) {
	// Test with a non-TTY environment (typical in tests/CI).
	info := InteractiveInfo{
		StdinTTY:  true,
		StdoutTTY: true,
		StderrTTY: true,
		NoPrompt:  false,
		CI:        false,
		Agent:     false,
	}
	if !info.CanPrompt() {
		t.Error("CanPrompt() with all conditions met = false, want true")
	}

	// Disable stdin TTY.
	info.StdinTTY = false
	if info.CanPrompt() {
		t.Error("CanPrompt() without stdin TTY = true, want false")
	}

	// Re-enable stdin, disable NoPrompt.
	info.StdinTTY = true
	info.NoPrompt = true
	if info.CanPrompt() {
		t.Error("CanPrompt() with NoPrompt = true, want false")
	}
}

// ---------------------------------------------------------------------------
// CanColorize
// ---------------------------------------------------------------------------

func TestCanColorize_ForceColor(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	// Remove NO_COLOR to avoid conflict.
	os.Unsetenv("NO_COLOR")

	info := InteractiveInfo{StdoutTTY: false}
	if !info.CanColorize() {
		t.Error("CanColorize() with FORCE_COLOR=1 = false, want true")
	}
}

func TestCanColorize_NoColor(t *testing.T) {
	os.Unsetenv("FORCE_COLOR")
	t.Setenv("NO_COLOR", "1")

	info := InteractiveInfo{StdoutTTY: true}
	if info.CanColorize() {
		t.Error("CanColorize() with NO_COLOR=1 = true, want false")
	}
}

func TestCanColorize_StdoutTTY(t *testing.T) {
	os.Unsetenv("FORCE_COLOR")
	os.Unsetenv("NO_COLOR")

	info := InteractiveInfo{StdoutTTY: true}
	if !info.CanColorize() {
		t.Error("CanColorize() with stdout TTY = false, want true")
	}

	info.StdoutTTY = false
	if info.CanColorize() {
		t.Error("CanColorize() without stdout TTY = true, want false")
	}
}

// ---------------------------------------------------------------------------
// Internal detection helpers
// ---------------------------------------------------------------------------

func TestIsNoPromptEnv(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"yes", true},
		{"YES", true},
		{"0", false},
		{"false", false},
		{"no", false},
		{"", false},
	}
	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("AZD_NO_PROMPT", tc.value)
			if got := isNoPromptEnv(); got != tc.want {
				t.Errorf("isNoPromptEnv() with %q = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestIsCIEnv(t *testing.T) {
	// Test each CI env var.
	for _, key := range ciEnvVars {
		t.Run(key, func(t *testing.T) {
			clearCIEnv(t)
			t.Setenv(key, "true")
			if !isCIEnv() {
				t.Errorf("isCIEnv() with %s=true = false, want true", key)
			}
		})
	}

	// Test with none set.
	t.Run("none", func(t *testing.T) {
		clearCIEnv(t)
		if isCIEnv() {
			t.Error("isCIEnv() with no CI vars = true, want false")
		}
	})
}

func TestIsAgentEnv(t *testing.T) {
	for _, key := range agentEnvVars {
		t.Run(key, func(t *testing.T) {
			clearAgentEnv(t)
			t.Setenv(key, "1")
			if !isAgentEnv() {
				t.Errorf("isAgentEnv() with %s=1 = false, want true", key)
			}
		})
	}

	t.Run("none", func(t *testing.T) {
		clearAgentEnv(t)
		if isAgentEnv() {
			t.Error("isAgentEnv() with no agent vars = true, want false")
		}
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func clearCIEnv(t *testing.T) {
	t.Helper()
	for _, key := range ciEnvVars {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
}

func clearAgentEnv(t *testing.T) {
	t.Helper()
	for _, key := range agentEnvVars {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
}

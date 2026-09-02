// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package terminal

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsTerminal_ForceTTY(t *testing.T) {
	clearTestEnvVars(t)

	// Test AZD_FORCE_TTY=true forces TTY mode
	t.Setenv("AZD_FORCE_TTY", "true")
	assert.True(t, IsTerminal(0, 0), "AZD_FORCE_TTY=true should force TTY mode")

	// Test AZD_FORCE_TTY=false forces non-TTY mode
	t.Setenv("AZD_FORCE_TTY", "false")
	assert.False(t, IsTerminal(0, 0), "AZD_FORCE_TTY=false should disable TTY mode")
}

func TestIsTerminal_CI(t *testing.T) {
	clearTestEnvVars(t)
	t.Setenv("CI", "1")

	assert.False(t, IsTerminal(0, 0), "CI should disable TTY mode")
}

func TestIsTerminal_NonTerminalFileDescriptors(t *testing.T) {
	clearTestEnvVars(t)
	nullFile, err := os.Open(os.DevNull)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, nullFile.Close())
	})

	assert.False(t, IsTerminal(nullFile.Fd(), nullFile.Fd()), "os.DevNull should not be a terminal")
}

func TestIsTerminal_AgentMarkerDoesNotOverrideTTY(t *testing.T) {
	clearTestEnvVars(t)
	t.Setenv("CLAUDECODE", "1")

	assert.True(t, isTerminal(0, 0, func(uintptr) bool {
		return true
	}),
		"agent attribution should not disable an interactive terminal")
}

// clearTestEnvVars clears environment variables that affect terminal detection.
func clearTestEnvVars(t *testing.T) {
	envVarsToUnset := []string{
		"AZD_FORCE_TTY",
		// Agent env vars
		"AI_AGENT", // GitHub Copilot hosts
		"CLAUDECODE",
		"CODEX_INTERNAL_ORIGINATOR_OVERRIDE", "CODEX_CI", "CODEX_THREAD_ID", "CODEX_SESSION_ID",
		"CURSOR_AGENT", "CURSOR_CONVERSATION_ID",
		"COPILOT_CLI",
		"GEMINI_CLI", "GEMINI_CLI_NO_RELAUNCH",
		"OPENCODE",
		// CI env vars
		"TF_BUILD", "GITHUB_ACTIONS", "APPVEYOR", "TRAVIS", "CIRCLECI", "GITLAB_CI",
		"CODEBUILD_BUILD_ID", "JENKINS_URL", "TEAMCITY_VERSION", "JB_SPACE_API_URL",
		"bamboo.buildKey", "BITBUCKET_BUILD_NUMBER", "CI", "BUILD_ID",
	}

	for _, envVar := range envVarsToUnset {
		if _, exists := os.LookupEnv(envVar); exists {
			t.Setenv(envVar, "")
			os.Unsetenv(envVar)
		}
	}
}

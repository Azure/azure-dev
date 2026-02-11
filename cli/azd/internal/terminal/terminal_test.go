// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package terminal

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
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

// clearTestEnvVars clears environment variables that affect terminal detection.
func clearTestEnvVars(t *testing.T) {
	envVarsToUnset := []string{
		"AZD_FORCE_TTY",
		// CI env vars
		"CI", "TF_BUILD", "GITHUB_ACTIONS",
	}

	for _, envVar := range envVarsToUnset {
		if _, exists := os.LookupEnv(envVar); exists {
			t.Setenv(envVar, "")
			os.Unsetenv(envVar)
		}
	}
}

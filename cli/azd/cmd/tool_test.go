// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolCommandGating(t *testing.T) {
	t.Run("hidden when alpha feature disabled", func(t *testing.T) {
		configDir := t.TempDir()
		t.Setenv("AZD_CONFIG_DIR", configDir)
		// Ensure the tool alpha feature is NOT enabled.
		t.Setenv("AZD_ALPHA_ENABLE_TOOL", "false")

		root := NewRootCmd(true, nil, nil)
		found := false
		for _, c := range root.Commands() {
			if c.Name() == "tool" {
				found = true
				break
			}
		}
		require.False(t, found, "expected 'tool' subcommand to be absent when alpha feature is disabled")
	})

	t.Run("present when alpha feature enabled", func(t *testing.T) {
		configDir := t.TempDir()
		t.Setenv("AZD_CONFIG_DIR", configDir)
		t.Setenv("AZD_ALPHA_ENABLE_TOOL", "true")

		root := NewRootCmd(true, nil, nil)
		found := false
		for _, c := range root.Commands() {
			if c.Name() == "tool" {
				found = true
				break
			}
		}
		require.True(t, found, "expected 'tool' subcommand to be present when alpha feature is enabled")
	})
}

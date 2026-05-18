// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/tool"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
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

func TestRunToolOperationUnsuccessfulResultReturnsError(t *testing.T) {
	toolDef := &tool.ToolDefinition{
		Id:   "az-cli",
		Name: "Azure CLI",
	}
	console := mockinput.NewMockConsole()

	results, err := runToolOperation(
		t.Context(),
		[]*tool.ToolDefinition{toolDef},
		func(ctx context.Context, ids []string) ([]*tool.InstallResult, error) {
			return []*tool.InstallResult{
				{
					Tool:    toolDef,
					Success: false,
				},
			}, nil
		},
		"Installing",
		"install",
		console,
	)

	require.Error(t, err)
	require.Len(t, results, 1)
	assert.False(t, results[0].Success)
	require.NotEmpty(t, console.Output())
	assert.Contains(t, console.Output()[0], "Some tools could not be")
}

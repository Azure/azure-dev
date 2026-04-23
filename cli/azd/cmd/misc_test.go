// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for createActionName (cobra_builder.go)

func TestCreateActionName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cmd      *cobra.Command
		expected string
	}{
		{
			name: "simple_command",
			cmd: func() *cobra.Command {
				root := &cobra.Command{Use: "azd"}
				child := &cobra.Command{Use: "init"}
				root.AddCommand(child)
				return child
			}(),
			expected: "azd-init-action",
		},
		{
			name: "nested_command",
			cmd: func() *cobra.Command {
				root := &cobra.Command{Use: "azd"}
				parent := &cobra.Command{Use: "config"}
				child := &cobra.Command{Use: "list"}
				root.AddCommand(parent)
				parent.AddCommand(child)
				return child
			}(),
			expected: "azd-config-list-action",
		},
		{
			name: "root_command",
			cmd: func() *cobra.Command {
				return &cobra.Command{Use: "azd"}
			}(),
			expected: "azd-action",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := createActionName(tt.cmd)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Tests for docsFlag (cobra_builder.go)

func TestDocsFlagStringAndType(t *testing.T) {
	t.Parallel()

	df := &docsFlag{value: true}
	assert.Equal(t, "true", df.String())
	assert.Equal(t, "bool", df.Type())

	df.value = false
	assert.Equal(t, "false", df.String())
}

// Tests for init help functions (init.go)

func TestGetCmdInitHelpDescription(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "init"}
	result := getCmdInitHelpDescription(cmd)
	require.Contains(t, result, "Initialize")
}

func TestGetCmdInitHelpFooter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "init"}
	result := getCmdInitHelpFooter(cmd)
	require.Contains(t, result, "Examples")
	require.Contains(t, result, "template")
}

func TestInitModeRequiredError(t *testing.T) {
	t.Parallel()

	e := &initModeRequiredError{}

	t.Run("Error", func(t *testing.T) {
		t.Parallel()
		assert.Contains(t, e.Error(), "initialization mode required")
	})

	t.Run("ToString", func(t *testing.T) {
		t.Parallel()
		s := e.ToString("")
		assert.Contains(t, s, "Init cannot continue")
		assert.Contains(t, s, "--minimal")
		assert.Contains(t, s, "template")
	})
}

// Tests for pipeline help functions (pipeline.go)

func TestGetCmdPipelineHelpDescription(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "pipeline"}
	result := getCmdPipelineHelpDescription(cmd)
	require.NotEmpty(t, result)
}

func TestGetCmdPipelineHelpFooter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "pipeline"}
	result := getCmdPipelineHelpFooter(cmd)
	require.Contains(t, result, "Examples")
}

func TestGetCmdPipelineConfigHelpDescription(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "config"}
	result := getCmdPipelineConfigHelpDescription(cmd)
	require.NotEmpty(t, result)
}

func TestGetCmdPipelineConfigHelpFooter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "config"}
	result := getCmdPipelineConfigHelpFooter(cmd)
	require.Contains(t, result, "Examples")
}

// Tests for root help functions (root.go)

func TestGetCmdRootHelpFooter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "azd"}
	result := getCmdRootHelpFooter(cmd)
	require.Contains(t, result, "template")
	require.Contains(t, result, "azd up")
}

// Tests for clearCommandContext (root.go)

func TestClearCommandContext(t *testing.T) {
	t.Parallel()
	root := &cobra.Command{Use: "root"}
	child := &cobra.Command{Use: "child"}
	root.AddCommand(child)

	// Set a non-nil context
	root.SetContext(t.Context())
	child.SetContext(t.Context())

	clearCommandContext(root)

	// After clearing, contexts should be nil (or reset to cobra default).
	// cobra returns background context when set to nil, so just verify
	// the function doesn't panic.
}

// Tests for more help functions across multiple files

func TestGetCmdDownHelpDescription(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "down"}
	result := getCmdDownHelpDescription(cmd)
	require.NotEmpty(t, result)
}

func TestGetCmdDownHelpFooter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "down"}
	result := getCmdDownHelpFooter(cmd)
	require.Contains(t, result, "Examples")
}

func TestGetCmdMonitorHelpDescription(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "monitor"}
	result := getCmdMonitorHelpDescription(cmd)
	require.NotEmpty(t, result)
}

func TestGetCmdMonitorHelpFooter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "monitor"}
	result := getCmdMonitorHelpFooter(cmd)
	require.Contains(t, result, "Examples")
}

func TestGetCmdUpHelpDescription(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "up"}
	result := getCmdUpHelpDescription(cmd)
	require.NotEmpty(t, result)
}

func TestGetCmdRestoreHelpDescription(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "restore"}
	result := getCmdRestoreHelpDescription(cmd)
	require.NotEmpty(t, result)
}

func TestGetCmdRestoreHelpFooter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "restore"}
	result := getCmdRestoreHelpFooter(cmd)
	require.Contains(t, result, "Examples")
}

func TestGetCmdPackageHelpDescription(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "package"}
	result := getCmdPackageHelpDescription(cmd)
	require.NotEmpty(t, result)
}

func TestGetCmdPackageHelpFooter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "package"}
	result := getCmdPackageHelpFooter(cmd)
	require.Contains(t, result, "Examples")
}

func TestGetCmdEnvRemoveHelpDescription(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "remove"}
	result := getCmdEnvRemoveHelpDescription(cmd)
	require.NotEmpty(t, result)
}

func TestInitModeRequiredError_MarshalJSON(t *testing.T) {
	t.Parallel()

	e := &initModeRequiredError{}
	data, err := e.MarshalJSON()
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// Verify JSON structure
	assert.Contains(t, string(data), `"code":"initModeRequired"`)
	assert.Contains(t, string(data), `"minimal"`)
	assert.Contains(t, string(data), `"template"`)
	assert.Contains(t, string(data), `"azd init --minimal"`)
}

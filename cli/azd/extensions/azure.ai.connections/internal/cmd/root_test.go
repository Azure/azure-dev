// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectionRootRejectsStandaloneDeploy(t *testing.T) {
	// Root construction changes Cobra's global traversal setting; do not run in parallel.
	for _, args := range [][]string{
		{"deploy"},
		{"deploy", "./connection.yaml"},
		{"deploy", "./connection.json", "--output", "json", "--no-prompt"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := NewRootCommand()
			preRunCalled := false
			root.PersistentPreRunE = func(*cobra.Command, []string) error {
				preRunCalled = true
				return nil
			}
			var output bytes.Buffer
			root.SetOut(&output)
			root.SetErr(&output)
			root.SetArgs(args)

			require.ErrorContains(t, root.ExecuteContext(t.Context()), `unknown command "deploy"`)
			assert.False(t, preRunCalled, "removed commands must fail before extension initialization")
		})
	}
}

func TestConnectionRootCommandSurface(t *testing.T) {
	root := NewRootCommand()
	for _, command := range root.Commands() {
		assert.NotEqual(t, "deploy", command.Name())
		assert.NotContains(t, command.Aliases, "deploy")
	}

	metadata := azdext.GenerateExtensionMetadata("1.0", "azure.ai.connections", root)
	var names []string
	for _, command := range metadata.Commands {
		require.Len(t, command.Name, 1)
		names = append(names, command.Name[0])
		assert.NotContains(t, command.Aliases, "deploy")
	}
	assert.NotContains(t, names, "deploy")
	for _, name := range []string{"create", "update", "delete", "list", "show"} {
		command, remaining, err := root.Find([]string{name})
		require.NoError(t, err)
		require.Empty(t, remaining)
		assert.Equal(t, name, command.Name())
		assert.NotNil(t, command.RunE)
		assert.Contains(t, names, name)
	}
	listen, remaining, err := root.Find([]string{"listen"})
	require.NoError(t, err)
	require.Empty(t, remaining)
	assert.True(t, listen.Hidden)
	assert.NotNil(t, listen.RunE)

	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"--help"})
	require.NoError(t, root.ExecuteContext(t.Context()))
	assert.Contains(t, output.String(), "create")
	assert.NotRegexp(t, `(?m)^\s+deploy\s`, output.String())
}

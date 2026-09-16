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

func TestToolboxRootRejectsStandaloneDeploy(t *testing.T) {
	// Root construction changes Cobra's global traversal setting; do not run in parallel.
	for _, args := range [][]string{
		{"deploy"},
		{"deploy", "./toolbox.yaml"},
		{"deploy", "./toolbox.json", "--output", "json", "--no-prompt"},
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

func TestToolboxRootCommandSurface(t *testing.T) {
	root := NewRootCommand()
	for _, command := range root.Commands() {
		assert.NotEqual(t, "deploy", command.Name())
		assert.NotContains(t, command.Aliases, "deploy")
	}

	metadata := azdext.GenerateExtensionMetadata("1.0", "azure.ai.toolboxes", root)
	var names []string
	for _, command := range metadata.Commands {
		require.Len(t, command.Name, 1)
		names = append(names, command.Name[0])
		assert.NotContains(t, command.Aliases, "deploy")
	}
	assert.NotContains(t, names, "deploy")
	for _, path := range [][]string{
		{"create"}, {"publish"}, {"delete"}, {"show"}, {"list"}, {"versions", "list"},
		{"add", "connection"}, {"add", "skill"},
		{"connection", "add"}, {"connection", "remove"},
		{"skill", "add"}, {"skill", "remove"},
	} {
		command, remaining, err := root.Find(path)
		require.NoError(t, err)
		require.Empty(t, remaining)
		assert.Equal(t, path[len(path)-1], command.Name())
		assert.NotNil(t, command.RunE)
		assert.Contains(t, names, path[0])
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

func TestToolboxCoreDeployServiceTargetRemainsRegistered(t *testing.T) {
	t.Parallel()
	// Creating a provider must not require a running daemon or contact Azure.
	host := azdext.NewExtensionHost(&azdext.AzdClient{})
	configureExtensionHost(host)
	targets := host.ServiceTargets()
	require.Len(t, targets, 1)
	require.Equal(t, aiToolboxHost, targets[0].Host)
	require.NotNil(t, targets[0].Factory)
	provider := targets[0].Factory()
	require.IsType(t, &toolboxServiceTarget{}, provider)
	require.NoError(t, provider.Initialize(t.Context(), &azdext.ServiceConfig{
		Name: "research-tools", Host: aiToolboxHost,
	}))
}

func TestToolboxLocalDefinitionHelp(t *testing.T) {
	root := NewRootCommand()
	for _, name := range []string{"add", "create"} {
		command, _, err := root.Find([]string{name})
		require.NoError(t, err)
		assert.NotContains(t, command.Long, "azd ai toolbox deploy")
	}
	add, _, err := root.Find([]string{"add"})
	require.NoError(t, err)
	assert.Contains(t, add.Long, "azd ai toolbox create <name> --from-file <path>")
	assert.Contains(t, add.Long, "azd deploy <service>")
	assert.Contains(t, fileShapeBlurb(true), "must match the positional name")
}

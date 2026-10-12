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

func TestCommandHelpUsesHostPrefix(t *testing.T) {
	for _, path := range []string{
		"", "dataset", "dataset create", "dataset versions", "dataset versions list",
		"init", "run", "run output", "run output list", "evaluator versions list", "version", "help",
	} {
		t.Run(path, func(t *testing.T) {
			root := NewRootCommand()
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs(append(strings.Fields(path), "--help", "--no-prompt"))

			require.NoError(t, root.ExecuteContext(t.Context()))
			commandPath := strings.TrimSpace("azd ai eval " + path)
			assert.Contains(t, stdout.String(), "\n  "+commandPath+" ")
			assert.Empty(t, stderr.String())
			if strings.Contains(stdout.String(), "Available Commands:") {
				assert.Contains(t, stdout.String(), `Use "`+commandPath+` [command] --help"`)
			}
			assert.NotContains(t, stdout.String(), "\n  eval ")
			assert.Equal(t, "eval", root.Name())
		})
	}
}

func TestCommandHelpSubcommandAndErrorsUseHostPrefix(t *testing.T) {
	root := NewRootCommand()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"help", "run", "output", "list"})
	require.NoError(t, root.ExecuteContext(t.Context()))
	assert.Contains(t, stdout.String(), "\n  azd ai eval run output list ")
	assert.Empty(t, stderr.String())

	root = NewRootCommand()
	root.SetArgs([]string{"not-a-command"})
	err := root.ExecuteContext(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `for "azd ai eval"`)

	root = NewRootCommand()
	root.SetArgs([]string{"dataset", "show", "--no-prompt"})
	err = root.ExecuteContext(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "azd ai eval dataset show")
}

func TestDatasetHelpExplainsSharedCatalog(t *testing.T) {
	cmd, _, err := NewRootCommand().Find([]string{"dataset"})
	require.NoError(t, err)
	var out bytes.Buffer
	cmd.SetOut(&out)
	require.NoError(t, cmd.Help())
	assert.Contains(t, out.String(), "azd ai dataset")
	assert.Contains(t, out.String(), "for standalone dataset management")
	assert.Contains(t, out.String(), "`azd ai eval dataset` (azure.ai.evaluations)")
	assert.Contains(t, out.String(), "same Foundry project dataset catalog")
	assert.Contains(t, out.String(), "when configured for the same project")
	assert.Contains(t, out.String(), "Command flags and output formats can differ.")
	assert.Contains(t, out.String(), "azure.ai.dataset")
	assert.Contains(t, out.String(), "azure.ai.evaluations")
	assert.Contains(t, out.String(), "azd ai eval generate")
}

func TestCommandHelpPreservesRoutingAndSDKOverrides(t *testing.T) {
	root := NewRootCommand()
	assert.Equal(t, "true", root.Annotations["azd-sdk-root"])
	cmd, remaining, err := root.Find([]string{"dataset", "show", "golden"})
	require.NoError(t, err)
	assert.Equal(t, "show", cmd.Name())
	assert.Equal(t, []string{"golden"}, remaining)

	version, _, err := root.Find([]string{"version"})
	require.NoError(t, err)
	originalDefault := root.PersistentFlags().Lookup("output").DefValue
	var out bytes.Buffer
	version.SetOut(&out)
	require.NoError(t, version.Help())
	assert.Contains(t, out.String(), "(supported: json)")
	assert.Equal(t, originalDefault, root.PersistentFlags().Lookup("output").DefValue)
	complete, ok := root.GetFlagCompletionFunc("output")
	require.True(t, ok)
	values, directive := complete(version, nil, "")
	assert.Equal(t, []string{"json"}, values)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)

	metadata := azdext.GenerateExtensionMetadata("1.0", "azure.ai.evaluations", root)
	for _, command := range metadata.Commands {
		assert.NotContains(t, command.Name, "azd")
		assert.NotContains(t, command.Name, "ai")
		assert.True(t, strings.HasPrefix(command.Usage, "azd ai eval "))
	}
}

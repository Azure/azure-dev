// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestAgentEvalMigration(t *testing.T) {
	for _, migration := range []struct {
		name        string
		replacement string
	}{
		{"", "azd ai eval --help"},
		{"init", "azd ai eval init"},
		{"generate", "azd ai eval generate"},
		{"run", "azd ai eval run start"},
		{"update", "azd deploy"},
		{"list", "azd ai eval --help"},
		{"show", "azd ai eval --help"},
	} {
		t.Run(migration.name, func(t *testing.T) {
			for _, invocation := range []struct {
				name string
				args []string
			}{
				{"bare", nil},
				{"positional argument", []string{"legacy-evaluation"}},
				{"old flags", []string{"--config", "legacy.yaml", "--reset-defaults"}},
				{"explicit empty old flag", []string{"--agent="}},
				{"non-interactive JSON", []string{"--no-prompt", "--output", "json"}},
				{"invalid global flag", []string{"--no-prompt=not-a-bool"}},
				{"credential-bearing endpoint", []string{
					"--project-endpoint", "https://private-user:private-password@example.com?sig=private-signature",
				}},
			} {
				t.Run(invocation.name, func(t *testing.T) {
					root := NewRootCommand()
					root.PersistentPreRunE = func(*cobra.Command, []string) error {
						t.Fatal("migration must fail before SDK, project, authentication, and telemetry pre-run hooks")
						return errors.New("unexpected pre-run")
					}
					root.PersistentPostRun = func(*cobra.Command, []string) {
						t.Fatal("migration must not run post-run hooks")
					}
					var output bytes.Buffer
					root.SetOut(&output)
					root.SetErr(&output)
					args := []string{"eval"}
					if migration.name != "" {
						args = append(args, migration.name)
					}
					root.SetArgs(append(args, invocation.args...))

					err := root.Execute()
					require.Error(t, err)
					localErr, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok, "expected a structured migration error, got %v", err)
					require.Equal(t, exterrors.CodeDeprecatedEvalCommand, localErr.Code)
					require.Equal(t, azdext.LocalErrorCategoryCompatibility, localErr.Category)
					require.Contains(t, localErr.Message, "deprecated")
					require.Contains(t, localErr.Suggestion, "azd extension install azure.ai.evaluations")
					require.Contains(t, localErr.Suggestion, "'"+migration.replacement+"'")
					require.Contains(t, output.String(), "deprecated")
					require.Contains(t, output.String(), migration.replacement)
					displayed := output.String() + err.Error() + localErr.Suggestion
					require.NotContains(t, displayed, "unknown command")
					require.NotContains(t, displayed, "unknown flag")
					require.NotContains(t, displayed, "private-")
					require.NotContains(t, displayed, "legacy.yaml")
				})
			}
		})
	}
}

func TestAgentEvalMigrationPreservesFiles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	files := map[string]string{
		"azure.yaml":                           "invalid: [",
		"eval.yaml":                            "legacy evaluation configuration",
		filepath.Join(".azure", "dev", ".env"): "LAST_EVAL_ID=existing-eval\n",
	}
	for path, content := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0600))
	}
	root := NewRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"eval", "generate", "--config", "eval.yaml", "--reset-defaults"})
	err := root.Execute()
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Equal(t, exterrors.CodeDeprecatedEvalCommand, localErr.Code)
	for path, content := range files {
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, content, string(after))
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, len(files))
}

func TestAgentEvalMigrationHelp(t *testing.T) {
	for _, name := range []string{"", "init", "generate", "run", "update", "list", "show"} {
		t.Run(name, func(t *testing.T) {
			root := NewRootCommand()
			var output bytes.Buffer
			root.SetOut(&output)
			root.SetErr(&output)
			args := []string{"eval"}
			if name != "" {
				args = append(args, name)
			}
			root.SetArgs(append(args, "--help"))
			require.NoError(t, root.Execute())
			require.Contains(t, output.String(), "migration guidance")
			require.Contains(t, output.String(), "azd extension install azure.ai.evaluations")
		})
	}
}

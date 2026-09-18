// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"azureaidataset/internal/foundry/projectctx"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestAIHelp(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	previous := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = previous })
	blockHelpContextReads(t)

	walkAIHelp(NewRootCommand(), nil, func(command *cobra.Command, path []string) {
		name := strings.Join(path, "_")
		if name == "" {
			name = "root"
		}
		t.Run(name, func(t *testing.T) {
			text, executed := executeAIHelp(t, path)
			require.NotContains(t, text, "\x1b")
			require.Contains(t, text, "\nUsage\n  azd ai "+command.CommandPath())
			require.Contains(t, text, "\nGlobal Flags\n")
			require.Equal(t, 1, strings.Count(text, "\nExamples\n"))
			for _, heading := range []string{"Usage", "Flags", "Global Flags", "Examples", "Available Commands"} {
				require.NotContains(t, text, "\n"+heading+":")
			}
			if len(path) == 0 {
				require.Contains(t, text, "\nProject Context\n")
				require.Contains(t, text, "\nEnvironments & Environment Variables\n")
				for _, key := range []string{
					"FOUNDRY_PROJECT_ENDPOINT", "AZURE_AI_PROJECT_ENDPOINT",
					"EVAL_DATASET_VERSION", "AZURE_AI_PROJECT_ID",
				} {
					require.Contains(t, text, key)
				}
			} else {
				require.NotContains(t, text, "\nEnvironments & Environment Variables\n")
			}
			for _, flags := range []*pflag.FlagSet{executed.Flags(), executed.InheritedFlags()} {
				flags.VisitAll(func(flag *pflag.Flag) {
					if !flag.Hidden && flag.Deprecated == "" {
						require.Contains(t, text, "--"+flag.Name)
					}
				})
			}
			lines := strings.Split(text, "\n")
			for i := range lines {
				lines[i] = strings.TrimRight(lines[i], " \t\r")
			}
			snapshot.SnapshotT(t, strings.TrimRight(strings.Join(lines, "\n"), "\n"))
		})
	})
}

func TestAIHelpColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	previous := color.NoColor
	color.NoColor = false
	t.Cleanup(func() { color.NoColor = previous })
	blockHelpContextReads(t)

	walkAIHelp(NewRootCommand(), nil, func(_ *cobra.Command, path []string) {
		t.Run(strings.Join(path, "_"), func(t *testing.T) {
			text, _ := executeAIHelp(t, path)
			require.Contains(t, text, output.WithBold("%s", output.WithUnderline("Usage")))
			require.Contains(t, text, output.WithBold("%s", output.WithUnderline("Examples")))
			require.Contains(t, text, output.WithHighLightFormat("%s", "--environment"))
			require.Contains(t, text, "\x1b[94m")
		})
	})
}

func TestAIHelpOutputOverrides(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	previous := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = previous })

	for _, path := range [][]string{
		{"create"}, {"update"}, {"list"}, {"show"}, {"download"}, {"delete"}, {"versions", "list"},
	} {
		t.Run(strings.Join(path, "_"), func(t *testing.T) {
			text, executed := executeAIHelp(t, path)
			require.Contains(t, text, `json, table`)
			require.Contains(t, text, `(default "table")`)
			// The SDK applies overrides only while rendering, then restores metadata.
			require.Equal(t, "default", executed.Flags().Lookup("output").DefValue)
		})
	}
}

func executeAIHelp(t *testing.T, path []string) (string, *cobra.Command) {
	t.Helper()
	root := NewRootCommand()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append(append([]string{}, path...), "--help"))
	executed, err := root.ExecuteC()
	require.NoError(t, err)
	return buf.String(), executed
}

func walkAIHelp(command *cobra.Command, path []string, visit func(*cobra.Command, []string)) {
	visit(command, path)
	for _, child := range command.Commands() {
		if child.IsAvailableCommand() {
			walkAIHelp(child, append(append([]string{}, path...), child.Name()), visit)
		}
	}
}

func blockHelpContextReads(t *testing.T) {
	t.Helper()
	previous := projectctx.ReadAzdHostedSourcesFunc
	projectctx.ReadAzdHostedSourcesFunc = func(context.Context) (projectctx.AzdHostedSources, error) {
		t.Fatal("help must not read project or environment state")
		return projectctx.AzdHostedSources{}, nil
	}
	t.Cleanup(func() { projectctx.ReadAzdHostedSourcesFunc = previous })
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "not-an-endpoint")
	t.Setenv("AZURE_AI_PROJECT_ENDPOINT", "not-an-endpoint")
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestAgentHelp(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	previous := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = previous })

	var walk func(*cobra.Command, []string)
	walk = func(command *cobra.Command, path []string) {
		name := strings.Join(path, "_")
		if name == "" {
			name = "root"
		}
		t.Run(name, func(t *testing.T) {
			root := NewRootCommand()
			var buf bytes.Buffer
			root.SetOut(&buf)
			root.SetErr(&buf)
			root.SetArgs(append(append([]string{}, path...), "--help"))
			require.NoError(t, root.Execute())
			text := buf.String()
			require.NotContains(t, text, "\x1b")
			require.Contains(t, text, "\nUsage\n  azd ai agent")
			require.Contains(t, text, "\nGlobal Flags\n")
			require.Contains(t, text, "\nExamples\n")
			require.Equal(t, 1, strings.Count(text, "\nExamples\n"))
			require.NotContains(t, text, "\nUsage:")
			require.NotContains(t, text, "\nFlags:")
			require.NotContains(t, text, "\nSubcommands:")
			if len(path) == 0 {
				require.Contains(t, text, "Environments & Environment Variables\n")
				require.Contains(t, text, ".azure/<environment>/.env")
				require.Contains(t, text, "${FOUNDRY_PROJECT_ENDPOINT}")
				require.Contains(t, text, "AGENT_<SERVICE>_NAME")
				require.Contains(t, text, "AGENT_<SERVICE>_VERSION")
				require.Contains(t, text, "AGENT_<SERVICE>_PROJECT_ENDPOINT")
			} else {
				require.NotContains(t, text, "Environments & Environment Variables")
				require.NotContains(t, text, bannerArt)
			}
			command.Flags().VisitAll(func(flag *pflag.Flag) {
				if !flag.Hidden && flag.Deprecated == "" {
					require.Contains(t, text, "--"+flag.Name)
				}
			})
			// Ignore trailing terminal padding (including the banner), but keep
			// indentation, internal whitespace and line breaks in snapshots.
			lines := strings.Split(text, "\n")
			for i, line := range lines {
				lines[i] = strings.TrimRight(line, " \t\r")
			}
			snapshot.SnapshotT(t, strings.TrimRight(strings.Join(lines, "\n"), "\n"))
		})
		for _, child := range command.Commands() {
			if child.IsAvailableCommand() {
				walk(child, append(append([]string{}, path...), child.Name()))
			}
		}
	}
	walk(NewRootCommand(), nil)
}

func TestAgentHelpColor(t *testing.T) {
	previous := color.NoColor
	color.NoColor = false
	t.Cleanup(func() { color.NoColor = previous })

	for _, path := range [][]string{nil, {"init"}, {"invoke"}, {"files", "list"}, {"optimize"}, {"eval", "show"}} {
		t.Run(strings.Join(path, "_"), func(t *testing.T) {
			root := NewRootCommand()
			var buf bytes.Buffer
			root.SetOut(&buf)
			root.SetErr(&buf)
			root.SetArgs(append(append([]string{}, path...), "--help"))
			require.NoError(t, root.Execute())
			text := buf.String()
			require.Contains(t, text, output.WithBold("%s", output.WithUnderline("Usage")))
			require.Contains(t, text, output.WithBold("%s", output.WithUnderline("Examples")))
			require.Contains(t, text, output.WithHighLightFormat("%s", "--environment"))
			require.Contains(t, text, "\x1b[94m")
			if len(path) == 0 {
				require.Contains(t, text,
					output.WithBold("%s", output.WithUnderline("Environments & Environment Variables")))
			}
		})
	}
}

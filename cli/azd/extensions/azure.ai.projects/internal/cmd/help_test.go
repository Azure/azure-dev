// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

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

	walkAIHelp(t, func(t *testing.T, command *cobra.Command, path []string) {
		text := executeAIHelp(t, path)
		require.NotContains(t, text, "\x1b")
		require.Contains(t, text, "\nUsage\n  azd ai "+command.CommandPath())
		require.Contains(t, text, "\nGlobal Flags\n")
		require.Equal(t, 1, strings.Count(text, "\nExamples\n"))
		require.NotEmpty(t, command.Example)
		require.NotContains(t, command.Long, "Examples:")
		for _, heading := range []string{"Usage:", "Flags:", "Subcommands:"} {
			require.NotContains(t, text, "\n"+heading)
		}
		if len(path) == 0 {
			require.Contains(t, text, "\nEnvironments & Environment Variables\n")
			require.Contains(t, text, ".azure/<environment>/.env")
			require.Contains(t, text, "FOUNDRY_PROJECT_ENDPOINT")
			require.Contains(t, text, "if the key is absent from .env")
			require.Contains(t, text, "explicitly empty persisted")
			require.Contains(t, text, "including when no environment is")
			require.Contains(t, text, "azd env set <key> <value>")
			require.NotContains(t, text, "azd env set <name> <value>")
		} else {
			require.NotContains(t, text, "\nEnvironments & Environment Variables\n")
		}
		checkFlag := func(flag *pflag.Flag) {
			row := `(?m)^ +(?:-[A-Za-z0-9], )?--` + regexp.QuoteMeta(flag.Name) + `(?:[ =]|$)`
			if flag.Hidden || flag.Deprecated != "" {
				require.NotRegexp(t, row, text)
			} else {
				require.Regexp(t, row, text)
			}
		}
		command.LocalFlags().VisitAll(checkFlag)
		command.InheritedFlags().VisitAll(checkFlag)
		for _, alias := range command.Aliases {
			aliasPath := append([]string{}, path...)
			aliasPath[len(aliasPath)-1] = alias
			require.Equal(t, text, executeAIHelp(t, aliasPath))
		}
		lines := strings.Split(text, "\n")
		for i, line := range lines {
			lines[i] = strings.TrimRight(line, " \t\r")
		}
		snapshot.SnapshotT(t, strings.TrimRight(strings.Join(lines, "\n"), "\n"))
	})
}

func TestAIHelpColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	previous := color.NoColor
	color.NoColor = false
	t.Cleanup(func() { color.NoColor = previous })

	walkAIHelp(t, func(t *testing.T, _ *cobra.Command, path []string) {
		text := executeAIHelp(t, path)
		require.Contains(t, text, output.WithBold("%s", output.WithUnderline("Usage")))
		require.Contains(t, text, output.WithBold("%s", output.WithUnderline("Examples")))
		require.Contains(t, text, output.WithHighLightFormat("%s", "--environment"))
		require.Contains(t, text, "\x1b[94m")
	})
}

func executeAIHelp(t *testing.T, path []string) string {
	t.Helper()
	root := NewRootCommand()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append(append([]string{}, path...), "--help"))
	require.NoError(t, root.Execute())
	return buf.String()
}

func walkAIHelp(t *testing.T, check func(*testing.T, *cobra.Command, []string)) {
	t.Helper()
	var walk func(*cobra.Command, []string)
	walk = func(command *cobra.Command, path []string) {
		name := strings.Join(path, "_")
		if name == "" {
			name = "root"
		}
		t.Run(name, func(t *testing.T) { check(t, command, path) })
		for _, child := range command.Commands() {
			if child.IsAvailableCommand() {
				walk(child, append(append([]string{}, path...), child.Name()))
			}
		}
	}
	walk(NewRootCommand(), nil)
}

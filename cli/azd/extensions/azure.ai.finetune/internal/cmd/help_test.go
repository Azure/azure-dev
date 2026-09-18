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

func TestAIHelp(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	previous := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = previous })

	walkAIHelp(t, func(t *testing.T, path []string) {
		text, command := executeAIHelp(t, path)
		require.NotContains(t, text, "\x1b")
		require.Contains(t, text, "\nUsage\n  azd ai finetuning")
		require.Contains(t, text, "\nGlobal Flags\n")
		require.Equal(t, 1, strings.Count(text, "\nExamples\n"))
		require.NotContains(t, text, "\nUsage:")
		require.NotContains(t, text, "\nFlags:")
		require.NotEmpty(t, command.Example)
		if len(path) == 0 {
			require.Contains(t, text, "Environments & Environment Variables\n")
			require.Contains(t, text, "AZURE_ACCOUNT_NAME")
		} else {
			require.NotContains(t, text, "Environments & Environment Variables")
		}
		for _, flags := range []*pflag.FlagSet{command.LocalFlags(), command.InheritedFlags()} {
			flags.VisitAll(func(flag *pflag.Flag) {
				if !flag.Hidden && flag.Deprecated == "" {
					require.Contains(t, text, "--"+flag.Name)
				}
			})
		}
		for _, child := range command.Commands() {
			if child.Hidden && child.Name() != "" {
				require.NotContains(t, text, "\n  "+child.Name()+" ")
			}
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
	walkAIHelp(t, func(t *testing.T, path []string) {
		text, _ := executeAIHelp(t, path)
		require.Contains(t, text, output.WithBold("%s", output.WithUnderline("Usage")))
		require.Contains(t, text, output.WithBold("%s", output.WithUnderline("Examples")))
		require.Contains(t, text, output.WithHighLightFormat("%s", "--debug"))
		require.Contains(t, text, "\x1b[94m")
	})
}

func walkAIHelp(t *testing.T, check func(*testing.T, []string)) {
	t.Helper()
	var walk func(*cobra.Command, []string)
	walk = func(command *cobra.Command, path []string) {
		name := strings.Join(path, "_")
		if name == "" {
			name = "root"
		}
		t.Run(name, func(t *testing.T) { check(t, path) })
		for _, child := range command.Commands() {
			if child.IsAvailableCommand() {
				walk(child, append(append([]string{}, path...), child.Name()))
			}
		}
	}
	walk(NewRootCommand(), nil)
}

func executeAIHelp(t *testing.T, path []string) (string, *cobra.Command) {
	t.Helper()
	root := NewRootCommand()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append(append([]string{}, path...), "--help"))
	require.NoError(t, root.Execute())
	command, _, err := root.Find(path)
	require.NoError(t, err)
	return buf.String(), command
}

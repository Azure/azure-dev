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
	"github.com/stretchr/testify/require"
)

func TestAIHelp(t *testing.T) {
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
			require.Contains(t, text, "\nUsage\n  azd ai builder")
			require.Contains(t, text, "\nGlobal Flags\n")
			require.Equal(t, 1, strings.Count(text, "\nExamples\n"))
			require.NotContains(t, text, "\x1b")
			snapshot.SnapshotT(t, strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n")))
		})
		for _, child := range command.Commands() {
			if child.IsAvailableCommand() {
				walk(child, append(append([]string{}, path...), child.Name()))
			}
		}
	}
	walk(NewRootCommand(), nil)
}

func TestAIHelpColor(t *testing.T) {
	previous := color.NoColor
	color.NoColor = false
	t.Cleanup(func() { color.NoColor = previous })
	for _, path := range [][]string{nil, {"start"}, {"version"}} {
		root := NewRootCommand()
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs(append(path, "--help"))
		require.NoError(t, root.Execute())
		require.Contains(t, buf.String(), output.WithBold("%s", output.WithUnderline("Usage")))
		require.Contains(t, buf.String(), output.WithHighLightFormat("%s", "--debug"))
	}
}

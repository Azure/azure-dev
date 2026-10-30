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

	"azureaiagent/internal/featureflags"
)

func TestAgentHelp(t *testing.T) {
	setPreviewProcess(t, "true")
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
				require.Contains(t, text, "Preview Feature Switches\n")
				for _, feature := range featureflags.All() {
					require.Contains(t, text, feature.EnvVar+"=true")
					require.Contains(t, text, feature.Description+".")
				}
				require.Contains(t, text, "All switches default to off.")
				require.Contains(t, text, "Only 1, t, T, true, TRUE, or True enable them.")
				require.Contains(t, text, "False, empty, and invalid values do not enable a switch.")
				require.Contains(t, text, "We recommend setting these in your shell or CI before running azd:")
				require.Contains(t, text, "The extension reads these switches from its process environment, including")
				require.Contains(t, text,
					"values the azd host already injected from the selected environment.")
				require.Contains(t, text, "It does not query azd config or load environment files from disk itself.")
				require.NotContains(t, text, "These declarations do not add command or runtime gates")
				require.Contains(t, text, "Regular hosted and plain prompt agents are outside these preview scopes.")
				require.NotContains(t, text, "AZD_FOUNDRY_EXT_ENABLE_")
			} else {
				require.NotContains(t, text, "Environments & Environment Variables")
				require.NotContains(t, text, "Preview Feature Switches")
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
			if child.IsAvailableCommand() || (!child.Hidden && child.Deprecated != "") {
				walk(child, append(append([]string{}, path...), child.Name()))
			}
		}
	}
	walk(NewRootCommand(), nil)
}

func TestAgentHelpColor(t *testing.T) {
	setPreviewProcess(t, "true")
	previous := color.NoColor
	color.NoColor = false
	t.Cleanup(func() { color.NoColor = previous })

	for _, path := range [][]string{nil, {"init"}, {"invoke"}, {"files", "list"}, {"optimize"}, {"eval", "run"}} {
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
				require.Contains(t, text,
					output.WithBold("%s", output.WithUnderline("Preview Feature Switches")))
			}
		})
	}
}

func TestAgentHelpSwitchListIndependentOfPreviewValues(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	previous := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = previous })

	var expected string
	for _, value := range []string{"", "false", "true", "invalid"} {
		for _, feature := range featureflags.All() {
			t.Setenv(feature.EnvVar, value)
		}
		root := NewRootCommand()
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"--help"})
		require.NoError(t, root.Execute())
		_, switches, found := strings.Cut(buf.String(), "\nPreview Feature Switches\n")
		require.True(t, found)
		if expected == "" {
			expected = switches
		}
		require.Equal(t, expected, switches)
	}
}

func TestInitHelpUsesTemplateFlag(t *testing.T) {
	setPreviewProcess(t, "true")
	t.Setenv("NO_COLOR", "1")
	previous := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = previous })

	root := NewRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"init", "--help"})

	require.NoError(t, root.Execute())
	text := output.String()
	require.Contains(t, text, "-t, --template string")
	require.NotContains(t, text, "-m, --manifest")
	require.Contains(t, text,
		"--manifest / -m is deprecated and retained for compatibility. Use\n--template / -t instead.")
}

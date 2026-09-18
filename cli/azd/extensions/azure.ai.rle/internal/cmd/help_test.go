// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestAIHelp(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv(rleEnableEnvVar, "true")
	previous := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = previous })

	walkAIHelp(t, func(t *testing.T, path []string) {
		text, command := executeAIHelp(t, path)
		require.NotContains(t, text, "\x1b")
		require.Contains(t, text, "\nUsage\n  azd ai rle")
		require.Contains(t, text, "\nGlobal Flags\n")
		require.Equal(t, 1, strings.Count(text, "\nExamples\n"))
		require.NotContains(t, text, "\nUsage:")
		require.NotContains(t, text, "\nFlags:")
		require.NotEmpty(t, command.Example)
		if len(path) == 0 {
			require.Contains(t, text, "Environment & Project Context\n")
			require.Contains(t, text, "AZD_AI_RLE_ENABLE=true")
			require.Contains(t, text, "FOUNDRY_PROJECT_ENDPOINT")
		} else {
			require.NotContains(t, text, "Environment & Project Context")
			require.NotContains(t, text, bannerArt)
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
	// Cached fatih/color styles retain NO_COLOR overrides from prior tests.
	// Re-execute this test in isolation rather than depending on test order.
	const helperEnv = "AZD_RLE_HELP_COLOR_TEST"
	if os.Getenv(helperEnv) != "1" {
		t.Setenv(helperEnv, "1")
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		executable, err := os.Executable()
		require.NoError(t, err)
		//nolint:gosec // Re-execute the current test binary with fixed arguments, not user input.
		command := exec.CommandContext(ctx, executable, "-test.run=^TestAIHelpColor$", "-test.count=1")
		result, err := command.CombinedOutput()
		require.NoError(t, err, "%s", result)
		return
	}
	t.Setenv(rleEnableEnvVar, "true")
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

func TestAIHelpDisabled(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv(rleEnableEnvVar, "false")
	previous := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = previous })
	text, root := executeAIHelp(t, nil)
	require.Contains(t, text, "AZD_AI_RLE_ENABLE=true")
	for _, child := range root.Commands() {
		if child.Hidden && child.Name() != "" {
			require.NotContains(t, text, "\n  "+child.Name()+" ")
		}
	}
	snapshot.SnapshotT(t, strings.TrimRight(text, "\r\n"))
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

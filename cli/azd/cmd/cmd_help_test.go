// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateCmdHelpDescription(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		title          string
		notes          []string
		expectContains []string
	}{
		{
			name:           "title_only",
			title:          "Simple command",
			notes:          nil,
			expectContains: []string{"Simple command"},
		},
		{
			name:  "title_with_notes",
			title: "Complex command",
			notes: []string{
				formatHelpNote("Note one"),
				formatHelpNote("Note two"),
			},
			expectContains: []string{
				"Complex command",
				"Note one",
				"Note two",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := generateCmdHelpDescription(tt.title, tt.notes)
			for _, s := range tt.expectContains {
				assert.Contains(t, result, s)
			}
		})
	}
}

func TestFormatHelpNote(t *testing.T) {
	t.Parallel()
	note := formatHelpNote("Remember to login first")
	require.Equal(t, "  • Remember to login first", note)
}

func TestGetPreFooter(t *testing.T) {
	t.Parallel()

	t.Run("with_subcommands", func(t *testing.T) {
		t.Parallel()
		parent := &cobra.Command{Use: "azd"}
		parent.AddCommand(&cobra.Command{Use: "init"})

		result := getPreFooter(parent)
		require.Contains(t, result, "azd")
		require.Contains(t, result, "--help")
	})

	t.Run("without_subcommands", func(t *testing.T) {
		t.Parallel()
		cmd := &cobra.Command{Use: "leaf"}

		result := getPreFooter(cmd)
		require.Empty(t, result)
	})
}

func TestGetCmdHelpDefaultFooter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	result := getCmdHelpDefaultFooter(cmd)
	require.Contains(t, result, "survey")
	require.Contains(t, result, "https://aka.ms/azure-dev/hats")
}

func TestGetCmdHelpDefaultDescription(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{
		Use:   "test",
		Short: "A test command",
	}
	result := getCmdHelpDefaultDescription(cmd)
	require.Contains(t, result, "A test command")
}

func TestGetCmdHelpDefaultUsage(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	result := getCmdHelpDefaultUsage(cmd)
	require.Contains(t, result, "Usage")
}

func TestGetCmdHelpCommands(t *testing.T) {
	t.Parallel()

	t.Run("empty_commands", func(t *testing.T) {
		t.Parallel()
		result := getCmdHelpCommands("Commands", "")
		require.Empty(t, result)
	})

	t.Run("with_commands", func(t *testing.T) {
		t.Parallel()
		result := getCmdHelpCommands("Commands", "  init : Initialize")
		require.Contains(t, result, "Commands")
		require.Contains(t, result, "init")
	})
}

func TestGetCmdHelpGroupedCommands(t *testing.T) {
	t.Parallel()
	result := getCmdHelpGroupedCommands("  init : Initialize")
	require.Contains(t, result, "Commands")
	require.Contains(t, result, "init")
}

func TestGetCmdHelpAvailableCommands(t *testing.T) {
	t.Parallel()
	result := getCmdHelpAvailableCommands("  init : Initialize")
	require.Contains(t, result, "Available Commands")
	require.Contains(t, result, "init")
}

func TestGetCommandsDetails(t *testing.T) {
	t.Parallel()

	t.Run("no_children", func(t *testing.T) {
		t.Parallel()
		cmd := &cobra.Command{Use: "parent"}
		result := getCommandsDetails(cmd)
		require.Empty(t, result)
	})

	t.Run("with_children", func(t *testing.T) {
		t.Parallel()
		noopRun := func(cmd *cobra.Command, args []string) {}
		parent := &cobra.Command{Use: "parent"}
		parent.AddCommand(&cobra.Command{
			Use:   "child-a",
			Short: "First child",
			Run:   noopRun,
		})
		parent.AddCommand(&cobra.Command{
			Use:   "child-b",
			Short: "Second child",
			Run:   noopRun,
		})

		result := getCommandsDetails(parent)
		require.Contains(t, result, "child-a")
		require.Contains(t, result, "First child")
		require.Contains(t, result, "child-b")
		require.Contains(t, result, "Second child")
	})
}

func TestAlignTitles(t *testing.T) {
	t.Parallel()
	lines := []string{
		"short" + endOfTitleSentinel + "desc A",
		"longer-title" + endOfTitleSentinel + "desc B",
	}
	max := len("longer-title" + endOfTitleSentinel)
	alignTitles(lines, max)

	// Both lines should have the ": " separator
	require.Contains(t, lines[0], "\t: desc A")
	require.Contains(t, lines[1], "\t: desc B")
}

func TestGetFlagsDetails(t *testing.T) {
	t.Parallel()

	t.Run("no_flags", func(t *testing.T) {
		t.Parallel()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		result := getFlagsDetails(fs)
		require.Empty(t, result)
	})

	t.Run("with_flags", func(t *testing.T) {
		t.Parallel()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.StringP("output", "o", "", "Output format")
		fs.Bool("verbose", false, "Enable verbose logging")

		result := getFlagsDetails(fs)
		require.Contains(t, result, "--output")
		require.Contains(t, result, "-o")
		require.Contains(t, result, "Output format")
		require.Contains(t, result, "--verbose")
	})

	t.Run("hidden_flags_excluded", func(t *testing.T) {
		t.Parallel()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("visible", "", "Visible flag")
		fs.String("hidden", "", "Hidden flag")
		_ = fs.MarkHidden("hidden")

		result := getFlagsDetails(fs)
		require.Contains(t, result, "visible")
		require.NotContains(t, result, "hidden")
	})
}

func TestGenerateCmdHelpSamplesBlock(t *testing.T) {
	t.Parallel()

	t.Run("empty_samples", func(t *testing.T) {
		t.Parallel()
		result := generateCmdHelpSamplesBlock(map[string]string{})
		require.Empty(t, result)
	})

	t.Run("with_samples", func(t *testing.T) {
		t.Parallel()
		samples := map[string]string{
			"Initialize a project": "azd init",
			"Deploy to Azure":      "azd up",
		}
		result := generateCmdHelpSamplesBlock(samples)
		require.Contains(t, result, "Examples")
		require.Contains(t, result, "Initialize a project")
		require.Contains(t, result, "azd init")
		require.Contains(t, result, "Deploy to Azure")
		require.Contains(t, result, "azd up")
	})
}

func TestGenerateCmdHelp(t *testing.T) {
	t.Parallel()

	t.Run("with_defaults", func(t *testing.T) {
		t.Parallel()
		cmd := &cobra.Command{
			Use:   "test",
			Short: "A test command",
		}

		result := generateCmdHelp(cmd, generateCmdHelpOptions{})
		require.Contains(t, result, "A test command")
		require.Contains(t, result, "Usage")
		require.Contains(t, result, "survey")
	})

	t.Run("with_custom_description", func(t *testing.T) {
		t.Parallel()
		cmd := &cobra.Command{
			Use:   "test",
			Short: "ignored",
		}

		result := generateCmdHelp(cmd, generateCmdHelpOptions{
			Description: func(c *cobra.Command) string {
				return "Custom description\n\n"
			},
		})
		require.Contains(t, result, "Custom description")
		require.NotContains(t, result, "ignored")
	})

	t.Run("with_custom_footer", func(t *testing.T) {
		t.Parallel()
		cmd := &cobra.Command{
			Use:   "test",
			Short: "Test cmd",
		}

		result := generateCmdHelp(cmd, generateCmdHelpOptions{
			Footer: func(c *cobra.Command) string {
				return "Custom footer text\n"
			},
		})
		require.Contains(t, result, "Custom footer text")
		require.NotContains(t, result, "survey")
	})
}

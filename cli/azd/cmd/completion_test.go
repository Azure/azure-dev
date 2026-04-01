// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestCompletionAction_SupportedShells(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		shell string
	}{
		{name: "bash", shell: shellBash},
		{name: "zsh", shell: shellZsh},
		{name: "fish", shell: shellFish},
		{name: "powershell", shell: shellPowerShell},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rootCmd := &cobra.Command{Use: "azd"}
			childCmd := &cobra.Command{Use: tt.shell}
			rootCmd.AddCommand(childCmd)

			action := &completionAction{
				shell: tt.shell,
				cmd:   childCmd,
			}

			result, err := action.Run(t.Context())
			require.NoError(t, err)
			require.NotNil(t, result)
		})
	}
}

func TestCompletionAction_UnsupportedShell(t *testing.T) {
	t.Parallel()

	rootCmd := &cobra.Command{Use: "azd"}
	childCmd := &cobra.Command{Use: "unsupported"}
	rootCmd.AddCommand(childCmd)

	action := &completionAction{
		shell: "unsupported",
		cmd:   childCmd,
	}

	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported shell")
}

func TestNewCompletionActions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		factory func(cmd *cobra.Command) interface{ Run(ctx any) }
		shell   string
	}{
		{
			name:  "bash_factory",
			shell: shellBash,
		},
		{
			name:  "zsh_factory",
			shell: shellZsh,
		},
		{
			name:  "fish_factory",
			shell: shellFish,
		},
		{
			name:  "powershell_factory",
			shell: shellPowerShell,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := &cobra.Command{Use: tt.shell}

			var action any
			switch tt.shell {
			case shellBash:
				action = newCompletionBashAction(cmd)
			case shellZsh:
				action = newCompletionZshAction(cmd)
			case shellFish:
				action = newCompletionFishAction(cmd)
			case shellPowerShell:
				action = newCompletionPowerShellAction(cmd)
			}

			require.NotNil(t, action)
			ca, ok := action.(*completionAction)
			require.True(t, ok)
			require.Equal(t, tt.shell, ca.shell)
			require.Same(t, cmd, ca.cmd)
		})
	}
}

func TestCompletionFigFlags(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "fig"}
	flags := newCompletionFigFlags(cmd)

	require.NotNil(t, flags)
	require.False(t, flags.includeHidden)

	// Verify flag is registered
	f := cmd.Flags().Lookup("include-hidden")
	require.NotNil(t, f)
}

func TestCompletionHelpDescriptions(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "test"}

	tests := []struct {
		name   string
		fn     func(*cobra.Command) string
		expect string
	}{
		{
			name:   "bash_description",
			fn:     getCmdCompletionBashHelpDescription,
			expect: "bash",
		},
		{
			name:   "bash_footer",
			fn:     getCmdCompletionBashHelpFooter,
			expect: "source",
		},
		{
			name:   "zsh_description",
			fn:     getCmdCompletionZshHelpDescription,
			expect: "zsh",
		},
		{
			name:   "zsh_footer",
			fn:     getCmdCompletionZshHelpFooter,
			expect: "source",
		},
		{
			name:   "fish_description",
			fn:     getCmdCompletionFishHelpDescription,
			expect: "fish",
		},
		{
			name:   "fish_footer",
			fn:     getCmdCompletionFishHelpFooter,
			expect: "source",
		},
		{
			name:   "powershell_description",
			fn:     getCmdCompletionPowerShellHelpDescription,
			expect: "PowerShell",
		},
		{
			name:   "powershell_footer",
			fn:     getCmdCompletionPowerShellHelpFooter,
			expect: "Invoke-Expression",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := tt.fn(cmd)
			require.NotEmpty(t, result)
			require.Contains(t, result, tt.expect)
		})
	}
}

func TestCompletionFigAction_Run(t *testing.T) {
	t.Parallel()

	root := &cobra.Command{Use: "azd", Short: "Azure Developer CLI"}
	child := &cobra.Command{Use: "init", Short: "Initialize", Run: func(cmd *cobra.Command, args []string) {}}
	root.AddCommand(child)

	action := newCompletionFigAction(root, &completionFigFlags{includeHidden: false}, nil)

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

func completionActions(root *actions.ActionDescriptor) {
	completionGroup := root.Add("completion", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short: "Generate shell completion scripts.",
			Long: `Generate shell completion scripts for azd.

The completion command allows you to generate autocompletion scripts for your shell,
currently supports bash, zsh, fish and PowerShell.

See each sub-command's help for details on how to use the generated script.`,
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupManage,
		},
	})

	completionGroup.Add("bash", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short:                 "Generate bash completion script.",
			DisableFlagsInUseLine: true,
		},
		ActionResolver: newCompletionBashAction,
		OutputFormats:  []output.Format{output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		HelpOptions: actions.ActionHelpOptions{
			Description: getCmdCompletionBashHelpDescription,
			Footer:      getCmdCompletionBashHelpFooter,
		},
	})

	completionGroup.Add("zsh", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short:                 "Generate zsh completion script.",
			DisableFlagsInUseLine: true,
		},
		ActionResolver: newCompletionZshAction,
		OutputFormats:  []output.Format{output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		HelpOptions: actions.ActionHelpOptions{
			Description: getCmdCompletionZshHelpDescription,
			Footer:      getCmdCompletionZshHelpFooter,
		},
	})

	completionGroup.Add("fish", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short:                 "Generate fish completion script.",
			DisableFlagsInUseLine: true,
		},
		ActionResolver: newCompletionFishAction,
		OutputFormats:  []output.Format{output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		HelpOptions: actions.ActionHelpOptions{
			Description: getCmdCompletionFishHelpDescription,
			Footer:      getCmdCompletionFishHelpFooter,
		},
	})

	completionGroup.Add("powershell", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short:                 "Generate PowerShell completion script.",
			DisableFlagsInUseLine: true,
		},
		ActionResolver: newCompletionPowerShellAction,
		OutputFormats:  []output.Format{output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		HelpOptions: actions.ActionHelpOptions{
			Description: getCmdCompletionPowerShellHelpDescription,
			Footer:      getCmdCompletionPowerShellHelpFooter,
		},
	})
}

type completionAction struct {
	shell string
	cmd   *cobra.Command
}

func newCompletionBashAction(cmd *cobra.Command) actions.Action {
	return &completionAction{shell: "bash", cmd: cmd}
}

func newCompletionZshAction(cmd *cobra.Command) actions.Action {
	return &completionAction{shell: "zsh", cmd: cmd}
}

func newCompletionFishAction(cmd *cobra.Command) actions.Action {
	return &completionAction{shell: "fish", cmd: cmd}
}

func newCompletionPowerShellAction(cmd *cobra.Command) actions.Action {
	return &completionAction{shell: "powershell", cmd: cmd}
}

func (a *completionAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	rootCmd := a.cmd
	for rootCmd.Parent() != nil {
		rootCmd = rootCmd.Parent()
	}

	var err error
	switch a.shell {
	case "bash":
		err = rootCmd.GenBashCompletion(a.cmd.OutOrStdout())
	case "zsh":
		err = rootCmd.GenZshCompletion(a.cmd.OutOrStdout())
	case "fish":
		err = rootCmd.GenFishCompletion(a.cmd.OutOrStdout(), true)
	case "powershell":
		err = rootCmd.GenPowerShellCompletion(a.cmd.OutOrStdout())
	default:
		return nil, fmt.Errorf("unsupported shell: %s", a.shell)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to generate %s completion: %w", a.shell, err)
	}

	return &actions.ActionResult{}, nil
}

// Help functions for completion commands

func getCmdCompletionBashHelpDescription(cmd *cobra.Command) string {
	return generateCmdHelpDescription("Generate bash completion script.", []string{
		"This script depends on the 'bash-completion' package.",
		"If it is not installed already, you can install it via your OS's package manager.",
	})
}

func getCmdCompletionBashHelpFooter(cmd *cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Load completions in current session":          "source <(azd completion bash)",
		"Install completions for all sessions (Linux)": "azd completion bash > /etc/bash_completion.d/azd",
		"Install completions for all sessions (macOS)": "azd completion bash > $(brew --prefix)/etc/bash_completion.d/azd",
	})
}

func getCmdCompletionZshHelpDescription(cmd *cobra.Command) string {
	return generateCmdHelpDescription("Generate zsh completion script.", []string{
		"If shell completion is not already enabled in your environment, you will need to enable it by running:",
		"",
		"echo \"autoload -U compinit; compinit\" >> ~/.zshrc",
	})
}

func getCmdCompletionZshHelpFooter(cmd *cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Load completions in current session":  "source <(azd completion zsh)",
		"Install completions for all sessions": "azd completion zsh > \"${fpath[1]}/_azd\"",
	})
}

func getCmdCompletionFishHelpDescription(cmd *cobra.Command) string {
	return generateCmdHelpDescription("Generate fish completion script.", nil)
}

func getCmdCompletionFishHelpFooter(cmd *cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Load completions in current session":  "azd completion fish | source",
		"Install completions for all sessions": "azd completion fish > ~/.config/fish/completions/azd.fish",
	})
}

func getCmdCompletionPowerShellHelpDescription(cmd *cobra.Command) string {
	return generateCmdHelpDescription("Generate PowerShell completion script.", nil)
}

func getCmdCompletionPowerShellHelpFooter(cmd *cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Load completions in current session":  "azd completion powershell | Out-String | Invoke-Expression",
		"Install completions for all sessions": "azd completion powershell >> $PROFILE",
	})
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal/figspec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

const (
	shellBash       = "bash"
	shellZsh        = "zsh"
	shellFish       = "fish"
	shellPowerShell = "powershell"
	shellFig        = "fig"
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

	completionGroup.Add(shellBash, &actions.ActionDescriptorOptions{
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

	completionGroup.Add(shellZsh, &actions.ActionDescriptorOptions{
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

	completionGroup.Add(shellFish, &actions.ActionDescriptorOptions{
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

	completionGroup.Add(shellPowerShell, &actions.ActionDescriptorOptions{
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

	figCmd := &cobra.Command{
		Short:                 "Generate Fig autocomplete spec.",
		DisableFlagsInUseLine: true,
	}
	completionGroup.Add(shellFig, &actions.ActionDescriptorOptions{
		Command:        figCmd,
		ActionResolver: newCompletionFigAction,
		FlagsResolver:  newCompletionFigFlags,
		OutputFormats:  []output.Format{output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
	})
}

type completionAction struct {
	shell string
	cmd   *cobra.Command
}

func newCompletionBashAction(cmd *cobra.Command) actions.Action {
	return &completionAction{shell: shellBash, cmd: cmd}
}

func newCompletionZshAction(cmd *cobra.Command) actions.Action {
	return &completionAction{shell: shellZsh, cmd: cmd}
}

func newCompletionFishAction(cmd *cobra.Command) actions.Action {
	return &completionAction{shell: shellFish, cmd: cmd}
}

func newCompletionPowerShellAction(cmd *cobra.Command) actions.Action {
	return &completionAction{shell: shellPowerShell, cmd: cmd}
}

// Fig completion action and flags
type completionFigFlags struct {
	includeHidden bool
}

func newCompletionFigFlags(cmd *cobra.Command) *completionFigFlags {
	flags := &completionFigFlags{}
	cmd.Flags().BoolVar(&flags.includeHidden, "include-hidden", false, "Include hidden commands in the Fig spec")
	_ = cmd.Flags().MarkHidden("include-hidden")
	return flags
}

type completionFigAction struct {
	flags            *completionFigFlags
	cmd              *cobra.Command
	extensionManager *extensions.Manager
}

func newCompletionFigAction(
	cmd *cobra.Command,
	flags *completionFigFlags,
	extensionManager *extensions.Manager,
) actions.Action {
	return &completionFigAction{
		flags:            flags,
		cmd:              cmd,
		extensionManager: extensionManager,
	}
}

func (a *completionAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	rootCmd := a.cmd.Root()

	var err error
	switch a.shell {
	case shellBash:
		err = rootCmd.GenBashCompletion(a.cmd.OutOrStdout())
	case shellZsh:
		err = rootCmd.GenZshCompletion(a.cmd.OutOrStdout())
	case shellFish:
		err = rootCmd.GenFishCompletion(a.cmd.OutOrStdout(), true)
	case shellPowerShell:
		err = rootCmd.GenPowerShellCompletion(a.cmd.OutOrStdout())
	default:
		return nil, fmt.Errorf("unsupported shell: %s", a.shell)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to generate %s completion: %w", a.shell, err)
	}

	return &actions.ActionResult{}, nil
}

func (a *completionFigAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	rootCmd := a.cmd.Root()

	// Generate the Fig spec
	builder := figspec.NewSpecBuilder(a.flags.includeHidden).
		WithExtensionMetadata(a.extensionManager)
	spec := builder.BuildSpec(rootCmd)

	// Convert to TypeScript
	tsCode, err := spec.ToTypeScript()
	if err != nil {
		return nil, fmt.Errorf("failed to generate Fig spec: %w", err)
	}

	// Write to stdout
	fmt.Fprint(a.cmd.OutOrStdout(), tsCode)

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
		"Load completions in current session": "azd completion powershell | Out-String | Invoke-Expression",
		"Install completions for all sessions": "azd completion powershell > $HOME\\.azd\\completion.ps1" +
			"\n    Add-Content $PROFILE \". $HOME\\.azd\\completion.ps1\"",
	})
}

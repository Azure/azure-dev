// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore helpformat
package cmd

import (
	"fmt"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/helpformat"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "toolbox",
		Use:   "toolbox <command> [options]",
		Short: "Manage Microsoft Foundry Toolboxes from your terminal. (Preview)",
		Long: `Manage Foundry toolboxes.

A toolbox is a versioned, named collection of connection-backed tools that
agents reference at run time. Each version is immutable: mutations (connection
add/remove, skill add/remove) create a new version but never change which
version is the default. Use 'azd ai toolbox publish <toolbox> <version>'
to promote a version.`,
	})

	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions = cobra.CompletionOptions{
		DisableDefaultCmd: true,
	}

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	sdkPreRun := rootCmd.PersistentPreRunE
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if err := validateRemoteFlags(cmd); err != nil {
			return err
		}
		if sdkPreRun != nil {
			return sdkPreRun(cmd, args)
		}
		return nil
	}

	// --output and --no-prompt are reserved azd globals and are inherited
	// automatically; only the extension-specific flag is registered here.
	rootCmd.PersistentFlags().String(
		"project-endpoint", "",
		"Foundry project endpoint URL for remote operations only (not local add or extension version).",
	)
	// Advertise the toolbox-specific --output allowed values + default on the
	// root so `azd ai toolbox --help` shows them too. Leaf commands re-register
	// on themselves; cobra annotations don't propagate.
	registerToolboxOutputFlag(rootCmd)

	rootCmd.AddCommand(newToolboxCreateCommand(extCtx))
	rootCmd.AddCommand(newToolboxAddCommand(extCtx))
	rootCmd.AddCommand(newToolboxPublishCommand(extCtx))
	rootCmd.AddCommand(newToolboxDeleteCommand(extCtx))
	rootCmd.AddCommand(newToolboxShowCommand(extCtx))
	rootCmd.AddCommand(newToolboxListCommand(extCtx))
	rootCmd.AddCommand(newToolboxVersionCommand(extCtx))
	rootCmd.AddCommand(newToolboxConnectionCommand(extCtx))
	rootCmd.AddCommand(newToolboxSkillCommand(extCtx))

	rootCmd.AddCommand(newVersionCommand(&extCtx.OutputFormat))
	rootCmd.AddCommand(newMetadataCommand(rootCmd))
	rootCmd.AddCommand(azdext.NewListenCommand(configureExtensionHost))

	rootCmd.Example = `  # Create a toolbox from a local definition
  azd ai toolbox create research --from-file ./toolbox.yaml

  # Inspect its versions before promoting a default
  azd ai toolbox versions list research
  azd ai toolbox publish research 2`
	helpformat.Install(rootCmd, "azd ai", toolboxHelpFooter)

	return rootCmd
}

func validateRemoteFlags(cmd *cobra.Command) error {
	command := cmd
	for command.Parent() != nil && command.Parent().Parent() != nil {
		command = command.Parent()
	}
	switch command.Name() {
	case "create", "publish", "delete", "show", "list", "versions", "connection", "skill":
		return nil
	}
	if cmd.Flags().Changed("project-endpoint") {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			fmt.Sprintf("--project-endpoint is not supported by 'azd ai %s'", cmd.CommandPath()),
			"remove --project-endpoint; this command does not use a project endpoint",
		)
	}
	return nil
}

// configureExtensionHost is the listen callback. It registers the
// azure.ai.toolbox service target so `azd up`/`azd deploy` upsert toolboxes
// declared as services in azure.yaml.
func configureExtensionHost(host *azdext.ExtensionHost) {
	azdClient := host.Client()
	host.WithServiceTarget(aiToolboxHost, func() azdext.ServiceTargetProvider {
		return newToolboxServiceTarget(azdClient)
	})
}

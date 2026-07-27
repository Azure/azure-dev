// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
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

	// --output and --no-prompt are reserved azd globals and are inherited
	// automatically; only the extension-specific flag is registered here.
	rootCmd.PersistentFlags().String(
		"project-endpoint", "",
		"Foundry project endpoint URL. When unset, falls back to the active azd "+
			"environment, azd user config, then FOUNDRY_PROJECT_ENDPOINT.",
	)
	// Advertise the toolbox-specific --output allowed values + default on the
	// root so `azd ai toolbox --help` shows them too. Leaf commands re-register
	// on themselves; cobra annotations don't propagate.
	registerToolboxOutputFlag(rootCmd)

	rootCmd.AddCommand(newToolboxCreateCommand(extCtx))
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

	return rootCmd
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

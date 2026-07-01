// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azure.ai.projects/pkg/provisioning"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "project",
		Use:   "project <command> [options]",
		Short: "Manage Microsoft Foundry Project resources from your terminal. (Preview)",
	})

	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions = cobra.CompletionOptions{
		DisableDefaultCmd: true,
	}

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	rootCmd.AddCommand(newContextCommand())
	rootCmd.AddCommand(newVersionCommand(&extCtx.OutputFormat))
	rootCmd.AddCommand(newMetadataCommand(rootCmd))
	rootCmd.AddCommand(newProjectSetCommand(extCtx))
	rootCmd.AddCommand(newProjectUnsetCommand(extCtx))
	rootCmd.AddCommand(newProjectShowCommand(extCtx))
	rootCmd.AddCommand(azdext.NewListenCommand(configureExtensionHost))

	return rootCmd
}

// configureExtensionHost is the listen callback. It registers both project-host
// surfaces: the azure.ai.project service target for `azd deploy` traversal and
// the microsoft.foundry provisioning provider for `azd provision`.
func configureExtensionHost(host *azdext.ExtensionHost) {
	azdClient := host.Client()
	host.
		WithServiceTarget(aiProjectHost, func() azdext.ServiceTargetProvider {
			return newProjectServiceTarget(azdClient)
		}).
		WithProvisioningProvider(provisioning.FoundryProviderName, func() azdext.ProvisioningProvider {
			return provisioning.NewFoundryProvisioningProvider(azdClient)
		})
}

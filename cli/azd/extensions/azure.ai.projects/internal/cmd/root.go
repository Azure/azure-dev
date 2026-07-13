// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
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

// configureExtensionHost is the listen callback. It registers the
// azure.ai.project service target so `azd up`/`azd deploy` can walk the project
// service declared in azure.yaml. The project itself is provisioned by the
// built-in microsoft.foundry Bicep provider, so the target is a no-op at deploy.
func configureExtensionHost(host *azdext.ExtensionHost) {
	azdClient := host.Client()
	host.WithServiceTarget(aiProjectHost, func() azdext.ServiceTargetProvider {
		return newProjectServiceTarget(azdClient)
	})
}

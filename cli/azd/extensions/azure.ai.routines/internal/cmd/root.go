// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "routine",
		Use:   "routine <command> [options]",
		Short: "Manage Microsoft Foundry Routines from your terminal. (Preview)",
	})

	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions = cobra.CompletionOptions{
		DisableDefaultCmd: true,
	}

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	// -p / --project-endpoint is a persistent flag so all subcommands inherit it.
	rootCmd.PersistentFlags().StringP("project-endpoint", "p", "",
		"Foundry project endpoint URL (overrides env var and config)")

	rootCmd.AddCommand(azdext.NewListenCommand(configureExtensionHost))
	rootCmd.AddCommand(newContextCommand())
	rootCmd.AddCommand(newVersionCommand(&extCtx.OutputFormat))
	rootCmd.AddCommand(newMetadataCommand(rootCmd))
	rootCmd.AddCommand(newRoutineCreateCommand(extCtx))
	rootCmd.AddCommand(newRoutineUpdateCommand(extCtx))
	rootCmd.AddCommand(newRoutineShowCommand(extCtx))
	rootCmd.AddCommand(newRoutineListCommand(extCtx))
	rootCmd.AddCommand(newRoutineDeleteCommand(extCtx))
	rootCmd.AddCommand(newRoutineEnableCommand(extCtx))
	rootCmd.AddCommand(newRoutineDisableCommand(extCtx))
	rootCmd.AddCommand(newRoutineDispatchCommand(extCtx))
	rootCmd.AddCommand(newRoutineRunCommand(extCtx))

	return rootCmd
}

// configureExtensionHost is the listen callback. It registers the
// azure.ai.routine service target so `azd up`/`azd deploy` upsert routines
// declared as services in azure.yaml.
func configureExtensionHost(host *azdext.ExtensionHost) {
	azdClient := host.Client()
	host.WithServiceTarget(aiRoutineHost, func() azdext.ServiceTargetProvider {
		return newRoutineServiceTarget(azdClient)
	})
}

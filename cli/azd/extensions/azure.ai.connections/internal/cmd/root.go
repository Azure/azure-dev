// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore helpformat
package cmd

import (
	"fmt"

	"azure.ai.connections/internal/exterrors"
	"azure.ai.connections/internal/helpformat"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "connection",
		Use:   "connection <command> [options]",
		Short: "Manage Microsoft Foundry Connections from your terminal. (Preview)",
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

	rootCmd.AddCommand(newContextCommand())
	rootCmd.AddCommand(newVersionCommand(&extCtx.OutputFormat))
	rootCmd.AddCommand(newMetadataCommand(rootCmd))
	rootCmd.AddCommand(azdext.NewListenCommand(func(host *azdext.ExtensionHost) {
		configureExtensionHostForEnvironment(host, extCtx.Environment)
	}))

	// Register -p / --project-endpoint as a persistent flag inherited by
	// connection CRUD subcommands (list, show, create, update, delete).
	rootCmd.PersistentFlags().StringP("project-endpoint", "p", "",
		"Foundry project endpoint URL for connection operations only (not context or version)")

	// Connection CRUD subcommands (migrated from the azure.ai.agents extension).
	rootCmd.AddCommand(newConnectionListCommand(extCtx))
	rootCmd.AddCommand(newConnectionShowCommand(extCtx))
	rootCmd.AddCommand(newConnectionCreateCommand(extCtx))
	rootCmd.AddCommand(newConnectionUpdateCommand(extCtx))
	rootCmd.AddCommand(newConnectionDeleteCommand(extCtx))

	rootCmd.Example = `  # List the connections in the resolved Foundry project
  azd ai connection list

  # Inspect a connection without displaying its credentials
  azd ai connection show my-search`
	helpformat.Install(rootCmd, "azd ai", connectionHelpFooter)

	return rootCmd
}

func validateRemoteFlags(cmd *cobra.Command) error {
	command := cmd
	for command.Parent() != nil && command.Parent().Parent() != nil {
		command = command.Parent()
	}
	switch command.Name() {
	case "list", "show", "create", "update", "delete":
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

// configureExtensionHostForEnvironment registers the azure.ai.connection target
// for `azd up`/`azd deploy`, preserving the environment selected by the caller.
func configureExtensionHostForEnvironment(host *azdext.ExtensionHost, environmentName string) {
	azdClient := host.Client()
	host.WithServiceTarget(aiConnectionHost, func() azdext.ServiceTargetProvider {
		return newConnectionServiceTarget(azdClient, environmentName)
	})
}

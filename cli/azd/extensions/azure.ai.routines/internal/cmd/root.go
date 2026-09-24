// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore helpformat
package cmd

import (
	"fmt"

	"azure.ai.routines/internal/exterrors"
	"azure.ai.routines/internal/helpformat"
	"azure.ai.routines/internal/pkg/routines"

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

	// -p / --project-endpoint is inherited by all subcommands.
	rootCmd.PersistentFlags().StringP("project-endpoint", "p", "",
		"Foundry project endpoint URL for remote operations only (not add, context, or version)")
	rootCmd.PersistentFlags().String(routineHTTPTimeoutFlag, "",
		fmt.Sprintf("HTTP request timeout override (for example, 2m or 90s). "+
			"Defaults to %s for reads and %s for writes. Not supported by add, context, or version.",
			routines.DefaultReadRequestTimeout, routines.DefaultWriteRequestTimeout))

	rootCmd.AddCommand(azdext.NewListenCommand(configureExtensionHost))
	rootCmd.AddCommand(newContextCommand())
	rootCmd.AddCommand(newVersionCommand(&extCtx.OutputFormat))
	rootCmd.AddCommand(newMetadataCommand(rootCmd))
	rootCmd.AddCommand(newRoutineAddCommand(extCtx))
	rootCmd.AddCommand(newRoutineCreateCommand(extCtx))
	rootCmd.AddCommand(newRoutineUpdateCommand(extCtx))
	rootCmd.AddCommand(newRoutineShowCommand(extCtx))
	rootCmd.AddCommand(newRoutineListCommand(extCtx))
	rootCmd.AddCommand(newRoutineDeleteCommand(extCtx))
	rootCmd.AddCommand(newRoutineEnableCommand(extCtx))
	rootCmd.AddCommand(newRoutineDisableCommand(extCtx))
	rootCmd.AddCommand(newRoutineDispatchCommand(extCtx))
	rootCmd.AddCommand(newRoutineRunCommand(extCtx))

	rootCmd.Example = `  # Create a routine from a manifest
  azd ai routine create nightly-summary --file ./routine.yaml

  # Trigger it manually and inspect execution history
  azd ai routine dispatch nightly-summary
  azd ai routine run list nightly-summary`
	helpformat.Install(rootCmd, "azd ai", routineHelpFooter)

	return rootCmd
}

func validateRemoteFlags(cmd *cobra.Command) error {
	command := cmd
	for command.Parent() != nil && command.Parent().Parent() != nil {
		command = command.Parent()
	}
	switch command.Name() {
	case "create", "update", "show", "list", "delete", "enable", "disable", "dispatch", "run":
		return nil
	}
	for _, name := range []string{"project-endpoint", routineHTTPTimeoutFlag} {
		if cmd.Flags().Changed(name) {
			return exterrors.Validation(
				exterrors.CodeConflictingArguments,
				fmt.Sprintf("--%s is not supported by 'azd ai %s'", name, cmd.CommandPath()),
				fmt.Sprintf("remove --%s; this command does not make routine HTTP requests", name),
			)
		}
	}
	return nil
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

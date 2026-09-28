// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore helpformat
package cmd

import (
	"context"

	"azure.ai.projects/internal/helpformat"
	"azure.ai.projects/internal/provisioning"

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
	rootCmd.AddCommand(newProjectAddCommand(extCtx))
	rootCmd.AddCommand(newProjectDeploymentCommand(extCtx))
	rootCmd.AddCommand(azdext.NewListenCommand(configureExtensionHost))

	rootCmd.Example = `  # Configure a Foundry project in the current azd project
  azd ai project add

  # Inspect the resolved project endpoint
  azd ai project show`
	helpformat.Install(rootCmd, "azd ai", projectHelpFooter)

	return rootCmd
}

// configureExtensionHost registers project lifecycle providers.
func configureExtensionHost(host *azdext.ExtensionHost) {
	azdClient := host.Client()
	host.
		WithServiceTarget(
			aiProjectHost,
			func() azdext.ServiceTargetProvider {
				return newProjectServiceTarget(azdClient)
			},
		).
		WithProvisioningProvider(
			provisioning.FoundryProviderName,
			func() azdext.ProvisioningProvider {
				return provisioning.NewFoundryProvisioningProvider(
					azdClient,
				)
			},
		).
		WithValidationCheck(azdext.ValidationCheckRegistration{
			CheckType: azdext.ValidationCheckTypeProvision,
			RuleID:    provisioning.ResourceGroupLocationRuleID,
			Factory: func() azdext.ValidationCheckProvider {
				return provisioning.NewResourceGroupLocationCheck(
					azdClient,
				)
			},
		}).
		WithProjectEventHandler(
			"preprovision",
			func(
				ctx context.Context,
				args *azdext.ProjectEventArgs,
			) error {
				return projectLifecycleHandler(ctx, azdClient, args)
			},
		).
		WithProjectEventHandler(
			"predeploy",
			func(
				ctx context.Context,
				args *azdext.ProjectEventArgs,
			) error {
				return projectLifecycleHandler(ctx, azdClient, args)
			},
		)
}

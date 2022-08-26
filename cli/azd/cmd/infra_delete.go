package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/iac/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/spin"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	bicepTool "github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func infraDeleteCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	return commands.Build(
		&infraDeleteAction{
			rootOptions: rootOptions,
		},
		rootOptions,
		"delete",
		"Delete Azure resources for an application.",
		"",
	)
}

type infraDeleteAction struct {
	forceDelete bool
	purgeDelete bool
	rootOptions *commands.GlobalCommandOptions
}

func (a *infraDeleteAction) SetupFlags(
	persis *pflag.FlagSet,
	local *pflag.FlagSet,
) {
	local.BoolVar(&a.forceDelete, "force", false, "Does not require confirmation before it deletes resources.")
	local.BoolVar(&a.purgeDelete, "purge", false, "Does not require confirmation before it permanently deletes resources that are soft-deleted by default (for example, key vaults).")
}

func (a *infraDeleteAction) Run(ctx context.Context, _ *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
	azCli := commands.GetAzCliFromContext(ctx)
	bicepCli := bicepTool.NewBicepCli(bicepTool.NewBicepCliArgs{AzCli: azCli})
	console := input.NewConsole(!a.rootOptions.NoPrompt)

	if err := ensureProject(azdCtx.ProjectPath()); err != nil {
		return err
	}

	if err := tools.EnsureInstalled(ctx, azCli, bicepCli); err != nil {
		return err
	}

	if err := ensureLoggedIn(ctx); err != nil {
		return fmt.Errorf("failed to ensure login: %w", err)
	}

	env, err := loadOrInitEnvironment(ctx, &a.rootOptions.EnvironmentName, azdCtx, console)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	const rootModule = "main"

	bicepPath := azdCtx.BicepModulePath(rootModule)

	// When we destroy the infrastructure, we want to remove any outputs from the deployment
	// that are in the environment. This allows templates to use outputs as "state" across deployment
	// that persists in the environment but is removed when the infrastructure is destroyed. This is
	// often exploited by container apps and not removing these outputs makes an `up`, `down`, `up` flow
	// fail.
	template, err := bicep.Compile(ctx, bicepCli, bicepPath)
	if err != nil {
		return fmt.Errorf("compiling template: %w", err)
	}

	resourceGroups, err := azureutil.GetResourceGroupsForDeployment(ctx, azCli, env.GetSubscriptionId(), env.GetEnvName())
	if err != nil {
		return fmt.Errorf("discovering resource groups from deployment: %w", err)
	}

	var allResources []azcli.AzCliResource

	for _, resourceGroup := range resourceGroups {
		resources, err := azCli.ListResourceGroupResources(ctx, env.GetSubscriptionId(), resourceGroup)
		if err != nil {
			return fmt.Errorf("listing resource group %s: %w", resourceGroup, err)
		}

		allResources = append(allResources, resources...)
	}

	if len(allResources) > 0 && !a.forceDelete {
		ok, err := console.Confirm(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf(
				"This will delete %d resources, are you sure you want to continue?\n"+
					"You can use --force to skip this confirmation.",
				len(allResources)),
			DefaultValue: false,
		})
		if err != nil {
			return fmt.Errorf("prompting for confirmation: %w", err)
		}
		if !ok {
			return nil
		}
	}

	// Azure KeyVaults have a "soft delete" functionality (now enabled by default) where a vault may be marked
	// such that when it is deleted it can be recovered for a period of time. During that time, the name may
	// not be reused.
	//
	// This means that running `az dev provision`, then `az dev infra delete` and finally `az dev provision`
	// again would lead to a deployment error since the vault name is in use.
	//
	// Since that's behavior we'd like to support, we run a purge operation for each KeyVault after
	// it has been deleted.
	//
	// See https://docs.microsoft.com/azure/key-vault/general/key-vault-recovery?tabs=azure-portal#what-are-soft-delete-and-purge-protection
	// for more information on this feature.
	var keyVaultsToPurge []string

	for _, resource := range allResources {
		if resource.Type == string(infra.AzureResourceTypeKeyVault) {
			vault, err := azCli.GetKeyVault(ctx, env.GetSubscriptionId(), resource.Name)
			if err != nil {
				return fmt.Errorf("listing keyvault %s properties: %w", resource.Name, err)
			}
			if vault.Properties.EnableSoftDelete && !vault.Properties.EnablePurgeProtection {
				keyVaultsToPurge = append(keyVaultsToPurge, resource.Name)
			}
		}
	}

	purgeDelete := a.purgeDelete

	if len(keyVaultsToPurge) > 0 && !purgeDelete {
		fmt.Printf(""+
			"This operation will delete and purge %d Key Vaults. These Key Vaults have soft delete enabled allowing them to be recovered for a period \n"+
			"of time after deletion. During this period, their names may not be reused.\n"+
			"You can use argument --purge to skip this confirmation.\n",
			len(keyVaultsToPurge))
		purgeDelete, err = console.Confirm(ctx, input.ConsoleOptions{
			Message:      "Would you like to *permanently* delete these Key Vaults instead, allowing their names to be reused?",
			DefaultValue: false,
		})

		if err != nil {
			return fmt.Errorf("prompting for purge confirmation: %w", err)
		}
	}

	// Do the deleting. The calls to `DeleteResourceGroup` and `DeleteSubscriptionDeployment` block
	// until everything has been deleted which can take a bit, so indicate we are working with a spinner.
	deleteFn := func() error {
		for _, resourceGroup := range resourceGroups {
			if err := azCli.DeleteResourceGroup(ctx, env.GetSubscriptionId(), resourceGroup); err != nil {
				return fmt.Errorf("deleting resource group %s: %w", resourceGroup, err)
			}
		}

		if purgeDelete {
			for _, vaultName := range keyVaultsToPurge {
				err := azCli.PurgeKeyVault(ctx, env.GetSubscriptionId(), vaultName)
				if err != nil {
					return fmt.Errorf("purging key vault %s: %w", vaultName, err)
				}
			}
		}

		if err := azCli.DeleteSubscriptionDeployment(ctx, env.GetSubscriptionId(), env.GetEnvName()); err != nil {
			return fmt.Errorf("deleting subscription deployment: %w", err)
		}
		return nil
	}

	spinner := spin.NewSpinner("Deleting Azure resources")
	if err := spinner.Run(deleteFn); err != nil {
		return fmt.Errorf("destroying: %w", err)
	}

	// Remove any outputs from the template from the environment since destroying the infrastructure
	// invalidated them all.
	for outputName := range template.Outputs {
		delete(env.Values, outputName)
	}

	if err := env.Save(); err != nil {
		return fmt.Errorf("saving environment: %w", err)
	}

	return nil
}

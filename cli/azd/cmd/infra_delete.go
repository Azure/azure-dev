package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/spin"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/theckman/yacspin"
)

func infraDeleteCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	return commands.Build(
		&infraDeleteAction{
			rootOptions: rootOptions,
		},
		rootOptions,
		"delete",
		"Delete Azure resources for an application",
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
	local.BoolVar(&a.forceDelete, "force", false, "Do not require confirmation before deleting resources")
	local.BoolVar(&a.purgeDelete, "purge", false, "Permanently delete resources which are soft-deleted by default (e.g. Key Vaults)")
}

func (a *infraDeleteAction) Run(ctx context.Context, _ *cobra.Command, args []string, azdCtx *environment.AzdContext) error {
	azCli := commands.GetAzCliFromContext(ctx)
	askOne := makeAskOne(a.rootOptions.NoPrompt)

	if err := ensureProject(azdCtx.ProjectPath()); err != nil {
		return err
	}

	if err := ensureLoggedIn(ctx); err != nil {
		return fmt.Errorf("failed to ensure login: %w", err)
	}

	env, err := loadOrInitEnvironment(ctx, &a.rootOptions.EnvironmentName, azdCtx, askOne)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	projectConfig, err := project.LoadProjectConfig(azdCtx.ProjectPath(), &environment.Environment{})
	if err != nil {
		return fmt.Errorf("loading project: %w", err)
	}

	// Default module name to "main"
	if projectConfig.Infra.Module == "" {
		projectConfig.Infra.Module = "main"
	}

	infraProvider, err := provisioning.NewInfraProvider(&env, azdCtx.ProjectDirectory(), projectConfig.Infra, azCli)
	if err != nil {
		return fmt.Errorf("error creating infra provider: %w", err)
	}

	requiredTools := infraProvider.RequiredExternalTools()
	if err := tools.EnsureInstalled(ctx, requiredTools...); err != nil {
		return err
	}

	// TODO: Purge keyvaults & confirmation

	// if len(allResources) > 0 && !a.forceDelete {
	// 	var ok bool
	// 	err := askOne(&survey.Confirm{
	// 		Message: fmt.Sprintf("This will delete %d resources, are you sure you want to continue?", len(allResources)),
	// 		Default: false,
	// 	}, &ok)
	// 	if err != nil {
	// 		return fmt.Errorf("prompting for confirmation: %w", err)
	// 	}
	// 	if !ok {
	// 		return nil
	// 	}
	// }

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
	//var keyVaultsToPurge []string

	// for _, resource := range allResources {
	// 	if resource.Type == string(infra.AzureResourceTypeKeyVault) {
	// 		vault, err := azCli.GetKeyVault(ctx, env.GetSubscriptionId(), resource.Name)
	// 		if err != nil {
	// 			return fmt.Errorf("listing keyvault %s properties: %w", resource.Name, err)
	// 		}
	// 		if vault.Properties.EnableSoftDelete && !vault.Properties.EnablePurgeProtection {
	// 			keyVaultsToPurge = append(keyVaultsToPurge, resource.Name)
	// 		}
	// 	}
	// }

	// purgeDelete := a.purgeDelete

	// if len(keyVaultsToPurge) > 0 && !purgeDelete {
	// 	fmt.Printf(""+
	// 		"This operation will delete %d Key Vaults. These Key Vaults have soft delete enabled allowing them to be recovered for a period \n"+
	// 		"of time after deletion. During this period, their names may not be reused.\n",
	// 		len(keyVaultsToPurge))
	// 	err := askOne(&survey.Confirm{
	// 		Message: "Would you like to *permanently* delete these Key Vaults instead, allowing their names to be reused?",
	// 		Default: false,
	// 	}, &purgeDelete)
	// 	if err != nil {
	// 		return fmt.Errorf("prompting for purge confirmation: %w", err)
	// 	}
	// }

	// Do the deleting. The calls to `DeleteResourceGroup` and `DeleteSubscriptionDeployment` block
	// until everything has been deleted which can take a bit, so indicate we are working with a spinner.
	deleteWithProgress := func(showProgress func(string)) error {
		planTask := infraProvider.Plan(ctx)

		go func() {
			for planProgress := range planTask.Progress() {
				showProgress(fmt.Sprintf("%s...", planProgress.Message))
			}
		}()

		planResult := planTask.Result()
		if planTask.Error != nil {
			return fmt.Errorf("creating destroy plan template: %w", err)
		}

		destroyTask := infraProvider.Destroy(ctx, &planResult.Plan)

		go func() {
			for destroyProgress := range destroyTask.Progress() {
				showProgress(fmt.Sprintf("%s...", destroyProgress.Message))
			}
		}()

		deployResult := destroyTask.Result()
		if destroyTask.Error != nil {
			return fmt.Errorf("error destroying resources: %w", destroyTask.Error)
		}

		// Remove any outputs from the template from the environment since destroying the infrastructure
		// invalidated them all.
		for outputName := range deployResult.Outputs {
			delete(env.Values, outputName)
		}

		if err := env.Save(); err != nil {
			return fmt.Errorf("saving environment: %w", err)
		}

		return nil
	}

	err = spin.RunWithUpdater("Deleting Azure resources ", deleteWithProgress,
		func(s *yacspin.Spinner, success bool) {
			var stopMessage string
			if success {
				stopMessage = "Deleted Azure resources"
			} else {
				stopMessage = "Error while deleting Azure resources"
			}

			s.StopMessage(stopMessage)
		})

	if err != nil {
		return fmt.Errorf("destroying: %w", err)
	}

	return nil
}

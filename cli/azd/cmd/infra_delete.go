package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func infraDeleteCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	return commands.Build(
		&infraDeleteAction{
			rootOptions: rootOptions,
		},
		rootOptions,
		"delete",
		"Delete Azure resources for an application.",
		nil,
	)
}

type infraDeleteAction struct {
	forceDelete bool
	purgeDelete bool
	rootOptions *internal.GlobalCommandOptions
}

func (a *infraDeleteAction) SetupFlags(
	persis *pflag.FlagSet,
	local *pflag.FlagSet,
) {
	local.BoolVar(&a.forceDelete, "force", false, "Does not require confirmation before it deletes resources.")
	local.BoolVar(&a.purgeDelete, "purge", false, "Does not require confirmation before it permanently deletes resources that are soft-deleted by default (for example, key vaults).")
}

func (a *infraDeleteAction) Run(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
	azCli := azcli.GetAzCli(ctx)
	console := input.GetConsole(ctx)

	if err := ensureProject(azdCtx.ProjectPath()); err != nil {
		return err
	}

	if err := tools.EnsureInstalled(ctx, azCli); err != nil {
		return err
	}

	if err := ensureLoggedIn(ctx); err != nil {
		return fmt.Errorf("failed to ensure login: %w", err)
	}

	env, ctx, err := loadOrInitEnvironment(ctx, &a.rootOptions.EnvironmentName, azdCtx, console)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	prj, err := project.LoadProjectConfig(azdCtx.ProjectPath(), env)
	if err != nil {
		return fmt.Errorf("loading project: %w", err)
	}

	infraManager, err := provisioning.NewManager(ctx, env, prj.Path, prj.Infra, !a.rootOptions.NoPrompt)
	if err != nil {
		return fmt.Errorf("creating provisioning manager: %w", err)
	}

	deploymentPlan, err := infraManager.Plan(ctx)
	if err != nil {
		return fmt.Errorf("planning destroy: %w", err)
	}

	destroyOptions := provisioning.NewDestroyOptions(a.forceDelete, a.purgeDelete)
	destroyResult, err := infraManager.Destroy(ctx, &deploymentPlan.Deployment, destroyOptions)
	if err != nil {
		return fmt.Errorf("destroying infrastructure: %w", err)
	}

	// Remove any outputs from the template from the environment since destroying the infrastructure
	// invalidated them all.
	for outputName := range destroyResult.Outputs {
		delete(env.Values, outputName)
	}

	if err := env.Save(); err != nil {
		return fmt.Errorf("saving environment: %w", err)
	}

	return nil
}

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type infraDeleteFlags struct {
	forceDelete bool
	purgeDelete bool
	global      *internal.GlobalCommandOptions
}

func (i *infraDeleteFlags) Setup(flags *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	flags.BoolVar(&i.forceDelete, "force", false, "Does not require confirmation before it deletes resources.")
	flags.BoolVar(&i.purgeDelete, "purge", false, "Does not require confirmation before it permanently deletes resources that are soft-deleted by default (for example, key vaults).")
	i.global = global
}

func infraDeleteCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *infraDeleteFlags) {
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete Azure resources for an application.",
	}

	idf := &infraDeleteFlags{}
	idf.Setup(cmd.Flags(), global)

	return cmd, idf
}

type infraDeleteAction struct {
	flags   infraDeleteFlags
	azdCtx  *azdcontext.AzdContext
	azCli   azcli.AzCli
	console input.Console
}

func newInfraDeleteAction(flags infraDeleteFlags, azdCtx *azdcontext.AzdContext, azCli azcli.AzCli, console input.Console) *infraDeleteAction {
	return &infraDeleteAction{
		flags:   flags,
		azdCtx:  azdCtx,
		azCli:   azCli,
		console: console,
	}
}

func (a *infraDeleteAction) Run(ctx context.Context) error {
	if err := ensureProject(a.azdCtx.ProjectPath()); err != nil {
		return err
	}

	if err := tools.EnsureInstalled(ctx, a.azCli); err != nil {
		return err
	}

	if err := ensureLoggedIn(ctx); err != nil {
		return fmt.Errorf("failed to ensure login: %w", err)
	}

	env, ctx, err := loadOrInitEnvironment(ctx, &a.flags.global.EnvironmentName, a.azdCtx, a.console)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	prj, err := project.LoadProjectConfig(a.azdCtx.ProjectPath(), env)
	if err != nil {
		return fmt.Errorf("loading project: %w", err)
	}

	infraManager, err := provisioning.NewManager(ctx, env, prj.Path, prj.Infra, !a.flags.global.NoPrompt)
	if err != nil {
		return fmt.Errorf("creating provisioning manager: %w", err)
	}

	deploymentPlan, err := infraManager.Plan(ctx)
	if err != nil {
		return fmt.Errorf("planning destroy: %w", err)
	}

	destroyOptions := provisioning.NewDestroyOptions(a.flags.forceDelete, a.flags.purgeDelete)
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

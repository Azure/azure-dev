package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alphafeatures"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type infraDeleteFlags struct {
	forceDelete bool
	purgeDelete bool
	global      *internal.GlobalCommandOptions
	envFlag
}

func (i *infraDeleteFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.BoolVar(&i.forceDelete, "force", false, "Does not require confirmation before it deletes resources.")
	local.BoolVar(
		&i.purgeDelete,
		"purge",
		false,
		//nolint:lll
		"Does not require confirmation before it permanently deletes resources that are soft-deleted by default (for example, key vaults).",
	)
	i.envFlag.Bind(local, global)
	i.global = global
}

func newInfraDeleteFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *infraDeleteFlags {
	flags := &infraDeleteFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newInfraDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete",
		Short:   "Delete Azure resources for an app.",
		Aliases: []string{"down"},
	}
}

type infraDeleteAction struct {
	flags               *infraDeleteFlags
	accountManager      account.Manager
	azCli               azcli.AzCli
	azdCtx              *azdcontext.AzdContext
	env                 *environment.Environment
	console             input.Console
	commandRunner       exec.CommandRunner
	projectConfig       *project.ProjectConfig
	alphaFeatureManager *alphafeatures.AlphaFeatureManager
}

func newInfraDeleteAction(
	flags *infraDeleteFlags,
	accountManager account.Manager,
	azCli azcli.AzCli,
	azdCtx *azdcontext.AzdContext,
	env *environment.Environment,
	projectConfig *project.ProjectConfig,
	console input.Console,
	commandRunner exec.CommandRunner,
	alphaFeatureManager *alphafeatures.AlphaFeatureManager,
) actions.Action {
	return &infraDeleteAction{
		flags:               flags,
		accountManager:      accountManager,
		azCli:               azCli,
		azdCtx:              azdCtx,
		env:                 env,
		console:             console,
		commandRunner:       commandRunner,
		projectConfig:       projectConfig,
		alphaFeatureManager: alphaFeatureManager,
	}
}

func (a *infraDeleteAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	infraManager, err := provisioning.NewManager(
		ctx,
		a.env,
		a.projectConfig.Path,
		a.projectConfig.Infra,
		a.console.IsUnformatted(),
		a.azCli,
		a.console,
		a.commandRunner,
		a.accountManager,
		a.alphaFeatureManager,
	)

	if err != nil {
		return nil, fmt.Errorf("creating provisioning manager: %w", err)
	}

	deploymentPlan, err := infraManager.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("planning destroy: %w", err)
	}

	destroyOptions := provisioning.NewDestroyOptions(a.flags.forceDelete, a.flags.purgeDelete)
	destroyResult, err := infraManager.Destroy(ctx, &deploymentPlan.Deployment, destroyOptions)
	if err != nil {
		return nil, fmt.Errorf("destroying infrastructure: %w", err)
	}

	// Remove any outputs from the template from the environment since destroying the infrastructure
	// invalidated them all.
	for outputName := range destroyResult.Outputs {
		delete(a.env.Values, outputName)
	}

	if err := a.env.Save(); err != nil {
		return nil, fmt.Errorf("saving environment: %w", err)
	}

	return nil, nil
}

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type downFlags struct {
	forceDelete bool
	purgeDelete bool
	global      *internal.GlobalCommandOptions
	envFlag
}

func (i *downFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
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

func newDownFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *downFlags {
	flags := &downFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Delete Azure resources for an application.",
	}
}

type downAction struct {
	flags          *downFlags
	accountManager account.Manager
	azCli          azcli.AzCli
	azdCtx         *azdcontext.AzdContext
	env            *environment.Environment
	console        input.Console
	commandRunner  exec.CommandRunner
	projectConfig  *project.ProjectConfig
}

func newDownAction(
	flags *downFlags,
	accountManager account.Manager,
	azCli azcli.AzCli,
	azdCtx *azdcontext.AzdContext,
	env *environment.Environment,
	projectConfig *project.ProjectConfig,
	console input.Console,
	commandRunner exec.CommandRunner,
) actions.Action {
	return &downAction{
		flags:          flags,
		accountManager: accountManager,
		azCli:          azCli,
		azdCtx:         azdCtx,
		env:            env,
		console:        console,
		commandRunner:  commandRunner,
		projectConfig:  projectConfig,
	}
}

func (a *downAction) Run(ctx context.Context) (*actions.ActionResult, error) {
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

func getCmdDownHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(fmt.Sprintf(
		"Delete Azure resources for an application. Running %s will not delete application"+
			" files on your local machine.", output.WithHighLightFormat("azd down")), nil)
}

func getCmdDownHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Delete all resources for an application." +
			" You will be prompted to confirm your decision.": output.WithHighLightFormat("azd down"),
		"Forcibly delete all applications resources without confirmation.": output.WithHighLightFormat("azd down --force"),
		"Permanently delete resources that are soft-deleted by default," +
			" without confirmation.": output.WithHighLightFormat("azd down --purge"),
	})
}

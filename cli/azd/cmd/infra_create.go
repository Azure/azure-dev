package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"go.uber.org/multierr"
)

type infraCreateFlags struct {
	noProgress bool
	global     *internal.GlobalCommandOptions
	*envFlag
}

func (i *infraCreateFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	i.bindNonCommon(local, global)
	i.bindCommon(local, global)
}

func (i *infraCreateFlags) bindNonCommon(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.BoolVar(&i.noProgress, "no-progress", false, "Suppresses progress information.")

	i.global = global
}

func (i *infraCreateFlags) bindCommon(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	i.envFlag = &envFlag{}
	i.envFlag.Bind(local, global)
}

func (i *infraCreateFlags) setCommon(envFlag *envFlag) {
	i.envFlag = envFlag
}

func newInfraCreateFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *infraCreateFlags {
	flags := &infraCreateFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newInfraCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "create",
		Aliases: []string{"provision"},
		Short:   "Provision the Azure resources for an app.",
		//nolint:lll
		Long: `Provision the Azure resources for an app.

The command prompts you for the following values:
- Environment name: The name of your environment.
- Azure location: The Azure location where your resources will be deployed.
- Azure subscription: The Azure subscription where your resources will be deployed.

Depending on what Azure resources are created, running this command might take a while. To view progress, go to the Azure portal and search for the resource group that contains your environment name.`,
	}
}

type infraCreateAction struct {
	flags         *infraCreateFlags
	azdCtx        *azdcontext.AzdContext
	projectConfig *project.ProjectConfig
	azCli         azcli.AzCli
	formatter     output.Formatter
	writer        io.Writer
	console       input.Console
	commandRunner exec.CommandRunner
}

func newInfraCreateAction(
	flags *infraCreateFlags,
	azdCtx *azdcontext.AzdContext,
	projectConfig *project.ProjectConfig,
	azCli azcli.AzCli,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	commandRunner exec.CommandRunner,
) actions.Action {
	return &infraCreateAction{
		flags:         flags,
		azdCtx:        azdCtx,
		projectConfig: projectConfig,
		azCli:         azCli,
		formatter:     formatter,
		writer:        writer,
		console:       console,
		commandRunner: commandRunner,
	}
}

func (i *infraCreateAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Command title
	i.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Provisioning Azure resources (azd provision)",
		TitleNote: "Provisioning Azure resources can take some time"},
	)

	env, err := loadOrInitEnvironment(ctx, &i.flags.environmentName, i.azdCtx, i.console, i.azCli)
	if err != nil {
		return nil, fmt.Errorf("loading environment: %w", err)
	}

	if err = i.projectConfig.Initialize(ctx, env, i.commandRunner); err != nil {
		return nil, err
	}

	infraManager, err := provisioning.NewManager(
		ctx,
		env,
		i.projectConfig.Path,
		i.projectConfig.Infra,
		i.console.IsUnformatted(),
		i.azCli,
		i.console,
		i.commandRunner,
	)
	if err != nil {
		return nil, fmt.Errorf("creating provisioning manager: %w", err)
	}

	deploymentPlan, err := infraManager.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("planning deployment: %w", err)
	}

	provisioningScope := infra.NewSubscriptionScope(
		i.azCli, env.GetLocation(), env.GetSubscriptionId(), env.GetEnvName(),
	)
	deployResult, err := infraManager.Deploy(ctx, deploymentPlan, provisioningScope)

	if err != nil {
		if i.formatter.Kind() == output.JsonFormat {
			stateResult, err := infraManager.State(ctx, provisioningScope)
			if err != nil {
				return nil, fmt.Errorf(
					"deployment failed and the deployment result is unavailable: %w",
					multierr.Combine(err, err),
				)
			}

			if err := i.formatter.Format(
				provisioning.NewEnvRefreshResultFromState(stateResult.State), i.writer, nil); err != nil {
				return nil, fmt.Errorf(
					"deployment failed and the deployment result could not be displayed: %w",
					multierr.Combine(err, err),
				)
			}
		}

		return nil, fmt.Errorf("deployment failed: %w", err)
	}

	for _, svc := range i.projectConfig.Services {
		eventArgs := project.ServiceLifecycleEventArgs{
			Project: i.projectConfig,
			Service: svc,
			Args: map[string]any{
				"bicepOutput": deployResult.Deployment.Outputs,
			},
		}

		if err := svc.RaiseEvent(ctx, project.ServiceEventEnvUpdated, eventArgs); err != nil {
			return nil, err
		}
	}

	if i.formatter.Kind() == output.JsonFormat {
		stateResult, err := infraManager.State(ctx, provisioningScope)
		if err != nil {
			return nil, fmt.Errorf(
				"deployment succeeded but the deployment result is unavailable: %w",
				multierr.Combine(err, err),
			)
		}

		if err := i.formatter.Format(
			provisioning.NewEnvRefreshResultFromState(stateResult.State), i.writer, nil); err != nil {
			return nil, fmt.Errorf(
				"deployment succeeded but the deployment result could not be displayed: %w",
				multierr.Combine(err, err),
			)
		}
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   "Your project has been provisioned!",
			FollowUp: getResourceGroupFollowUp(ctx, i.formatter, i.azCli, i.projectConfig, env),
		},
	}, nil
}

package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
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
	//deprecate:Flag hide --no-progress
	local.MarkHidden("no-progress")
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
	flags           *infraCreateFlags
	accountManager  account.Manager
	projectManager  project.ProjectManager
	resourceManager project.ResourceManager
	azdCtx          *azdcontext.AzdContext
	azCli           azcli.AzCli
	env             *environment.Environment
	formatter       output.Formatter
	projectConfig   *project.ProjectConfig
	writer          io.Writer
	console         input.Console
	commandRunner   exec.CommandRunner
}

func newInfraCreateAction(
	flags *infraCreateFlags,
	accountManager account.Manager,
	projectManager project.ProjectManager,
	resourceManager project.ResourceManager,
	azdCtx *azdcontext.AzdContext,
	projectConfig *project.ProjectConfig,
	azCli azcli.AzCli,
	env *environment.Environment,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	commandRunner exec.CommandRunner,
) actions.Action {
	return &infraCreateAction{
		flags:           flags,
		accountManager:  accountManager,
		projectManager:  projectManager,
		resourceManager: resourceManager,
		azdCtx:          azdCtx,
		azCli:           azCli,
		env:             env,
		formatter:       formatter,
		projectConfig:   projectConfig,
		writer:          writer,
		console:         console,
		commandRunner:   commandRunner,
	}
}

func (i *infraCreateAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if i.flags.noProgress {
		fmt.Fprint(
			i.console.Handles().Stderr,
			//nolint:Lll
			output.WithWarningFormat("--no-progress flag is deprecated and will be removed."))
	}

	// Command title
	i.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Provisioning Azure resources (azd provision)",
		TitleNote: "Provisioning Azure resources can take some time"},
	)

	if err := i.projectManager.Initialize(ctx, i.projectConfig); err != nil {
		return nil, err
	}

	infraManager, err := provisioning.NewManager(
		ctx,
		i.env,
		i.projectConfig.Path,
		i.projectConfig.Infra,
		i.console.IsUnformatted(),
		i.azCli,
		i.console,
		i.commandRunner,
		i.accountManager,
	)
	if err != nil {
		return nil, fmt.Errorf("creating provisioning manager: %w", err)
	}

	deploymentPlan, err := infraManager.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("planning deployment: %w", err)
	}

	provisioningScope := infra.NewSubscriptionScope(
		i.azCli, i.env.GetLocation(), i.env.GetSubscriptionId(), i.env.GetEnvName(),
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
			FollowUp: getResourceGroupFollowUp(ctx, i.formatter, i.azCli, i.projectConfig, i.resourceManager, i.env),
		},
	}, nil
}

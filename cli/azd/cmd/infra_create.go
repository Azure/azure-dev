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
		Short:   "Create Azure resources for an app.",
		Aliases: []string{"provision"},
	}
}

type infraCreateAction struct {
	flags         *infraCreateFlags
	azCli         azcli.AzCli
	formatter     output.Formatter
	writer        io.Writer
	console       input.Console
	commandRunner exec.CommandRunner
}

func newInfraCreateAction(
	flags *infraCreateFlags,
	azCli azcli.AzCli,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	commandRunner exec.CommandRunner,
) actions.Action {
	return &infraCreateAction{
		flags:         flags,
		azCli:         azCli,
		formatter:     formatter,
		writer:        writer,
		console:       console,
		commandRunner: commandRunner,
	}
}

func (i *infraCreateAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// We call `NewAzdContext` here instead of having the value injected because we want to delay the
	// walk for the context until this command has started to execute (for example, in the case of `up`,
	// the context is not created until the init action actually runs, which is after the infraCreateAction
	// object is created.
	azdCtx, err := azdcontext.NewAzdContext()
	if err != nil {
		return nil, err
	}

	// Command title
	i.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Provisioning Azure resources (azd provision)",
		TitleNote: "Provisioning Azure resources can take some time"},
	)

	env, err := loadOrInitEnvironment(ctx, &i.flags.environmentName, azdCtx, i.console, i.azCli)
	if err != nil {
		return nil, fmt.Errorf("loading environment: %w", err)
	}

	prj, err := project.LoadProjectConfig(azdCtx.ProjectPath())
	if err != nil {
		return nil, fmt.Errorf("loading project: %w", err)
	}

	if err = prj.Initialize(ctx, env, i.commandRunner); err != nil {
		return nil, err
	}

	infraManager, err := provisioning.NewManager(
		ctx, env, prj.Path, prj.Infra, i.console.IsUnformatted(), i.azCli, i.console, i.commandRunner,
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

	for _, svc := range prj.Services {
		if err := svc.RaiseEvent(
			ctx, project.EnvironmentUpdated,
			map[string]any{"bicepOutput": deployResult.Deployment.Outputs}); err != nil {
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
			FollowUp: getResourceGroupFollowUp(ctx, i.formatter, i.azCli, prj, env),
		},
	}, nil
}

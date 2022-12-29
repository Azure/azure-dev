package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
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
	noProgress   bool
	outputFormat *string // pointer to allow delay-initialization when used in "azd up"
	global       *internal.GlobalCommandOptions
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

	i.outputFormat = convert.RefOf("")
	output.AddOutputFlag(
		local,
		i.outputFormat,
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat)
}

func (i *infraCreateFlags) setCommon(outputFormat *string, envFlag *envFlag) {
	i.envFlag = envFlag
	i.outputFormat = outputFormat
}

func infraCreateCmdDesign(rootOptions *internal.GlobalCommandOptions) (*cobra.Command, *infraCreateFlags) {
	cmd := &cobra.Command{
		Use:     "create",
		Short:   "Create Azure resources for an application.",
		Aliases: []string{"provision"},
	}
	f := &infraCreateFlags{}
	f.Bind(cmd.Flags(), rootOptions)

	return cmd, f
}

type infraCreateAction struct {
	flags         infraCreateFlags
	azCli         azcli.AzCli
	azdCtx        *azdcontext.AzdContext
	formatter     output.Formatter
	writer        io.Writer
	console       input.Console
	commandRunner exec.CommandRunner
}

func newInfraCreateAction(
	f infraCreateFlags,
	azCli azcli.AzCli,
	azdCtx *azdcontext.AzdContext,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	commandRunner exec.CommandRunner,
) *infraCreateAction {
	return &infraCreateAction{
		flags:         f,
		azCli:         azCli,
		azdCtx:        azdCtx,
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

	prj, err := project.LoadProjectConfig(i.azdCtx.ProjectPath())
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

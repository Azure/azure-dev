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
	// If set, redirects the final command printout to the channel
	finalOutputRedirect *[]string
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
		flags:               f,
		azCli:               azCli,
		azdCtx:              azdCtx,
		formatter:           formatter,
		writer:              writer,
		console:             console,
		commandRunner:       commandRunner,
		finalOutputRedirect: nil,
	}
}

func (i *infraCreateAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if err := ensureProject(i.azdCtx.ProjectPath()); err != nil {
		return nil, err
	}

	env, ctx, err := loadOrInitEnvironment(ctx, &i.flags.environmentName, i.azdCtx, i.console, i.azCli)
	if err != nil {
		return nil, fmt.Errorf("loading environment: %w", err)
	}

	prj, err := project.LoadProjectConfig(i.azdCtx.ProjectPath(), env)
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
		return nil, fmt.Errorf("deploying infrastructure: %w", err)
	}

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

	if i.formatter.Kind() != output.JsonFormat {
		resourceGroupName, err := project.GetResourceGroupName(ctx, i.azCli, prj, env)
		if err == nil { // Presentation only -- skip print if we failed to resolve the resource group
			i.displayResourceGroupCreatedMessage(ctx, i.console, env.GetSubscriptionId(), resourceGroupName)
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

	return nil, nil
}

func (ica *infraCreateAction) displayResourceGroupCreatedMessage(
	ctx context.Context,
	console input.Console,
	subscriptionId string,
	resourceGroup string,
) {
	resourceGroupCreatedMessage := resourceGroupCreatedMessage(ctx, subscriptionId, resourceGroup)
	if ica.finalOutputRedirect != nil {
		*ica.finalOutputRedirect = append(*ica.finalOutputRedirect, resourceGroupCreatedMessage)
	} else {
		console.Message(ctx, resourceGroupCreatedMessage)
	}
}

func resourceGroupCreatedMessage(ctx context.Context, subscriptionId string, resourceGroup string) string {
	resourcesGroupURL := fmt.Sprintf(
		"https://portal.azure.com/#@/resource/subscriptions/%s/resourceGroups/%s/overview",
		subscriptionId,
		resourceGroup)

	return fmt.Sprintf(
		"View the resources created under the resource group %s in Azure Portal:\n%s\n",
		output.WithHighLightFormat(resourceGroup),
		output.WithLinkFormat(resourcesGroupURL),
	)
}

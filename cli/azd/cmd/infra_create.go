package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"go.uber.org/multierr"
)

type infraCreateFlags struct {
	noProgress   bool
	outputFormat string
	global       *internal.GlobalCommandOptions
}

func (i *infraCreateFlags) Setup(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.BoolVar(&i.noProgress, "no-progress", false, "Suppresses progress information.")
	output.AddOutputFlag(
		local,
		&i.outputFormat,
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat)

	i.global = global
}

func infraCreateCmdDesign(rootOptions *internal.GlobalCommandOptions) (*cobra.Command, *infraCreateFlags) {
	cmd := &cobra.Command{
		Use:     "create",
		Short:   "Create Azure resources for an application.",
		Aliases: []string{"provision"},
	}
	f := &infraCreateFlags{}
	f.Setup(cmd.Flags(), rootOptions)

	return cmd, f
}

type infraCreateAction struct {
	flags     infraCreateFlags
	azdCtx    *azdcontext.AzdContext
	azCli     azcli.AzCli
	formatter output.Formatter
	writer    io.Writer
	console   input.Console
	// If set, redirects the final command printout to the channel
	finalOutputRedirect *[]string
}

func newInfraCreateAction(f infraCreateFlags, azdCtx *azdcontext.AzdContext, azCli azcli.AzCli, console input.Console, formatter output.Formatter, writer io.Writer) *infraCreateAction {
	return &infraCreateAction{
		flags:               f,
		azdCtx:              azdCtx,
		azCli:               azCli,
		formatter:           formatter,
		writer:              writer,
		console:             console,
		finalOutputRedirect: nil,
	}
}

func (ica *infraCreateAction) Run(ctx context.Context) error {
	if err := ensureProject(ica.azdCtx.ProjectPath()); err != nil {
		return err
	}

	if err := tools.EnsureInstalled(ctx, ica.azCli); err != nil {
		return err
	}

	if err := ensureLoggedIn(ctx); err != nil {
		return fmt.Errorf("failed to ensure login: %w", err)
	}

	env, ctx, err := loadOrInitEnvironment(ctx, &ica.flags.global.EnvironmentName, ica.azdCtx, ica.console)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	prj, err := project.LoadProjectConfig(ica.azdCtx.ProjectPath(), env)
	if err != nil {
		return fmt.Errorf("loading project: %w", err)
	}

	if err = prj.Initialize(ctx, env); err != nil {
		return err
	}

	infraManager, err := provisioning.NewManager(ctx, env, prj.Path, prj.Infra, !ica.flags.global.NoPrompt)
	if err != nil {
		return fmt.Errorf("creating provisioning manager: %w", err)
	}

	deploymentPlan, err := infraManager.Plan(ctx)
	if err != nil {
		return fmt.Errorf("planning deployment: %w", err)
	}

	provisioningScope := infra.NewSubscriptionScope(ctx, env.GetLocation(), env.GetSubscriptionId(), env.GetEnvName())
	deployResult, err := infraManager.Deploy(ctx, deploymentPlan, provisioningScope)
	if err != nil {
		return fmt.Errorf("deploying infrastructure: %w", err)
	}

	if err != nil {
		if ica.formatter.Kind() == output.JsonFormat {
			deployment, err := infraManager.GetDeployment(ctx, provisioningScope)
			if err != nil {
				return fmt.Errorf("deployment failed and the deployment result is unavailable: %w", multierr.Combine(err, err))
			}

			if err := ica.formatter.Format(deployment, ica.writer, nil); err != nil {
				return fmt.Errorf("deployment failed and the deployment result could not be displayed: %w", multierr.Combine(err, err))
			}
		}

		return fmt.Errorf("deployment failed: %w", err)
	}

	for _, svc := range prj.Services {
		if err := svc.RaiseEvent(ctx, project.Deployed, map[string]any{"bicepOutput": deployResult.Deployment.Outputs}); err != nil {
			return err
		}
	}

	resourceGroupName, err := project.GetResourceGroupName(ctx, prj, env)
	if err == nil { // Presentation only -- skip print if we failed to resolve the resource group
		ica.displayResourceGroupCreatedMessage(ctx, ica.console, env.GetSubscriptionId(), resourceGroupName)
	}

	return nil
}

func (ica *infraCreateAction) displayResourceGroupCreatedMessage(ctx context.Context, console input.Console, subscriptionId string, resourceGroup string) {
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

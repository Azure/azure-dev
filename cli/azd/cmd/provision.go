package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
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

type provisionFlags struct {
	noProgress bool
	global     *internal.GlobalCommandOptions
	*envFlag
}

func (i *provisionFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	i.bindNonCommon(local, global)
	i.bindCommon(local, global)
}

func (i *provisionFlags) bindNonCommon(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.BoolVar(&i.noProgress, "no-progress", false, "Suppresses progress information.")
	//deprecate:Flag hide --no-progress
	_ = local.MarkHidden("no-progress")
	i.global = global
}

func (i *provisionFlags) bindCommon(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	i.envFlag = &envFlag{}
	i.envFlag.Bind(local, global)
}

func (i *provisionFlags) setCommon(envFlag *envFlag) {
	i.envFlag = envFlag
}

func newProvisionFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *provisionFlags {
	flags := &provisionFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newProvisionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "provision",
		Short: "Provision the Azure resources for an application.",
		//nolint:lll
		Long: `Provision the Azure resources for an application.

The command prompts you for the following values:
- Environment name: The name of your environment.
- Azure location: The Azure location where your resources will be deployed.
- Azure subscription: The Azure subscription where your resources will be deployed.

Depending on what Azure resources are created, running this command might take a while. To view progress, go to the Azure portal and search for the resource group that contains your environment name.`,
	}
}

type provisionAction struct {
	flags               *provisionFlags
	accountManager      account.Manager
	projectManager      project.ProjectManager
	resourceManager     project.ResourceManager
	azdCtx              *azdcontext.AzdContext
	azCli               azcli.AzCli
	env                 *environment.Environment
	formatter           output.Formatter
	projectConfig       *project.ProjectConfig
	writer              io.Writer
	console             input.Console
	commandRunner       exec.CommandRunner
	userProfileService  *azcli.UserProfileService
	subResolver         account.SubscriptionTenantResolver
	alphaFeatureManager *alpha.FeatureManager
}

func newProvisionAction(
	flags *provisionFlags,
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
	userProfileService *azcli.UserProfileService,
	subResolver account.SubscriptionTenantResolver,
	alphaFeatureManager *alpha.FeatureManager,
) actions.Action {
	return &provisionAction{
		flags:               flags,
		accountManager:      accountManager,
		projectManager:      projectManager,
		resourceManager:     resourceManager,
		azdCtx:              azdCtx,
		azCli:               azCli,
		env:                 env,
		formatter:           formatter,
		projectConfig:       projectConfig,
		writer:              writer,
		console:             console,
		commandRunner:       commandRunner,
		userProfileService:  userProfileService,
		subResolver:         subResolver,
		alphaFeatureManager: alphaFeatureManager,
	}
}

func (p *provisionAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if p.flags.noProgress {
		fmt.Fprintln(
			p.console.Handles().Stderr,
			//nolint:Lll
			output.WithWarningFormat(
				"WARNING: The '--no-progress' flag is deprecated and will be removed in a future release.",
			),
		)
	}

	// Command title
	p.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Provisioning Azure resources (azd provision)",
		TitleNote: "Provisioning Azure resources can take some time"},
	)

	if err := p.projectManager.Initialize(ctx, p.projectConfig); err != nil {
		return nil, err
	}

	infraManager, err := provisioning.NewManager(
		ctx,
		p.env,
		p.projectConfig.Path,
		p.projectConfig.Infra,
		p.console.IsUnformatted(),
		p.azCli,
		p.console,
		p.commandRunner,
		p.accountManager,
		p.userProfileService,
		p.subResolver,
		p.alphaFeatureManager,
	)
	if err != nil {
		return nil, fmt.Errorf("creating provisioning manager: %w", err)
	}

	deploymentPlan, err := infraManager.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("planning deployment: %w", err)
	}

	deployResult, err := infraManager.Deploy(ctx, deploymentPlan)

	if err != nil {
		if p.formatter.Kind() == output.JsonFormat {
			stateResult, err := infraManager.State(ctx)
			if err != nil {
				return nil, fmt.Errorf(
					"deployment failed and the deployment result is unavailable: %w",
					multierr.Combine(err, err),
				)
			}

			if err := p.formatter.Format(
				provisioning.NewEnvRefreshResultFromState(stateResult.State), p.writer, nil); err != nil {
				return nil, fmt.Errorf(
					"deployment failed and the deployment result could not be displayed: %w",
					multierr.Combine(err, err),
				)
			}
		}

		return nil, fmt.Errorf("deployment failed: %w", err)
	}

	for _, svc := range p.projectConfig.Services {
		eventArgs := project.ServiceLifecycleEventArgs{
			Project: p.projectConfig,
			Service: svc,
			Args: map[string]any{
				"bicepOutput": deployResult.Deployment.Outputs,
			},
		}

		if err := svc.RaiseEvent(ctx, project.ServiceEventEnvUpdated, eventArgs); err != nil {
			return nil, err
		}
	}

	if p.formatter.Kind() == output.JsonFormat {
		stateResult, err := infraManager.State(ctx)
		if err != nil {
			return nil, fmt.Errorf(
				"deployment succeeded but the deployment result is unavailable: %w",
				multierr.Combine(err, err),
			)
		}

		if err := p.formatter.Format(
			provisioning.NewEnvRefreshResultFromState(stateResult.State), p.writer, nil); err != nil {
			return nil, fmt.Errorf(
				"deployment succeeded but the deployment result could not be displayed: %w",
				multierr.Combine(err, err),
			)
		}
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Your project has been provisioned!",
			FollowUp: getResourceGroupFollowUp(
				ctx, p.formatter, p.projectConfig, p.resourceManager, p.env),
		},
	}, nil
}

func getCmdProvisionHelpDescription(c *cobra.Command) string {
	return generateCmdHelpDescription(fmt.Sprintf(
		"Provision the Azure resources for an application."+
			" This step may take a while depending on the resources provisioned."+
			" You should run %s any time you update your Bicep or Terraform file."+
			"\n\nThis command prompts you to input the following:",
		output.WithHighLightFormat(c.CommandPath())), []string{
		formatHelpNote("Environment name: The name of your environment (ex: dev, test, prod)."),
		formatHelpNote("Azure location: The Azure location where your resources will be deployed."),
		formatHelpNote("Azure subscription: The Azure subscription where your resources will be deployed."),
	})
}

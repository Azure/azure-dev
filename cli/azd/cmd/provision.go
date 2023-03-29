package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
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

type provisionFlags struct {
	noProgress bool
	global     *internal.GlobalCommandOptions
	*envFlag
}

func (i *provisionFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	existing := local.Lookup("no-progress")
	if existing != nil {
		return
	}

	local.BoolVar(&i.noProgress, "no-progress", false, "Suppresses progress information.")
	//deprecate:Flag hide --no-progress
	_ = local.MarkHidden("no-progress")
}

func newProvisionFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *provisionFlags {
	flags := &provisionFlags{
		global:  global,
		envFlag: newEnvFlag(cmd, global),
	}
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
	flags                    *provisionFlags
	accountManager           account.Manager
	projectManager           project.ProjectManager
	resourceManager          project.ResourceManager
	azdCtx                   *azdcontext.AzdContext
	azCli                    azcli.AzCli
	env                      *environment.Environment
	formatter                output.Formatter
	projectConfig            *project.ProjectConfig
	writer                   io.Writer
	console                  input.Console
	commandRunner            exec.CommandRunner
	middlewareRunner         middleware.MiddlewareContext
	packageActionInitializer actions.ActionInitializer[*packageAction]
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
	middlewareRunner middleware.MiddlewareContext,
	packageActionInitializer actions.ActionInitializer[*packageAction],
) actions.Action {
	return &provisionAction{
		flags:                    flags,
		accountManager:           accountManager,
		projectManager:           projectManager,
		resourceManager:          resourceManager,
		azdCtx:                   azdCtx,
		azCli:                    azCli,
		env:                      env,
		formatter:                formatter,
		projectConfig:            projectConfig,
		writer:                   writer,
		console:                  console,
		commandRunner:            commandRunner,
		middlewareRunner:         middlewareRunner,
		packageActionInitializer: packageActionInitializer,
	}
}

func (pa *provisionAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if pa.flags.noProgress {
		fmt.Fprintf(
			pa.console.Handles().Stderr,
			//nolint:Lll
			output.WithWarningFormat("--no-progress flag is deprecated and will be removed in the future.")+"\n")
	}

	packageAction, err := pa.packageActionInitializer()
	if err != nil {
		return nil, err
	}

	packageOptions := &middleware.Options{CommandPath: "package"}
	_, err = pa.middlewareRunner.RunChildAction(ctx, packageOptions, packageAction)
	if err != nil {
		return nil, err
	}

	// Command title
	pa.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Provisioning Azure resources (azd provision)",
		TitleNote: "Provisioning Azure resources can take some time"},
	)

	if err := pa.projectManager.Initialize(ctx, pa.projectConfig); err != nil {
		return nil, err
	}

	infraManager, err := provisioning.NewManager(
		ctx,
		pa.env,
		pa.projectConfig.Path,
		pa.projectConfig.Infra,
		pa.console.IsUnformatted(),
		pa.azCli,
		pa.console,
		pa.commandRunner,
		pa.accountManager,
	)
	if err != nil {
		return nil, fmt.Errorf("creating provisioning manager: %w", err)
	}

	deploymentPlan, err := infraManager.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("planning deployment: %w", err)
	}

	provisioningScope := infra.NewSubscriptionScope(
		pa.azCli, pa.env.GetLocation(), pa.env.GetSubscriptionId(), pa.env.GetEnvName(),
	)
	deployResult, err := infraManager.Deploy(ctx, deploymentPlan, provisioningScope)

	if err != nil {
		if pa.formatter.Kind() == output.JsonFormat {
			stateResult, err := infraManager.State(ctx, provisioningScope)
			if err != nil {
				return nil, fmt.Errorf(
					"deployment failed and the deployment result is unavailable: %w",
					multierr.Combine(err, err),
				)
			}

			if err := pa.formatter.Format(
				provisioning.NewEnvRefreshResultFromState(stateResult.State), pa.writer, nil); err != nil {
				return nil, fmt.Errorf(
					"deployment failed and the deployment result could not be displayed: %w",
					multierr.Combine(err, err),
				)
			}
		}

		return nil, fmt.Errorf("deployment failed: %w", err)
	}

	for _, svc := range pa.projectConfig.Services {
		eventArgs := project.ServiceLifecycleEventArgs{
			Project: pa.projectConfig,
			Service: svc,
			Args: map[string]any{
				"bicepOutput": deployResult.Deployment.Outputs,
			},
		}

		if err := svc.RaiseEvent(ctx, project.ServiceEventEnvUpdated, eventArgs); err != nil {
			return nil, err
		}
	}

	if pa.formatter.Kind() == output.JsonFormat {
		stateResult, err := infraManager.State(ctx, provisioningScope)
		if err != nil {
			return nil, fmt.Errorf(
				"deployment succeeded but the deployment result is unavailable: %w",
				multierr.Combine(err, err),
			)
		}

		if err := pa.formatter.Format(
			provisioning.NewEnvRefreshResultFromState(stateResult.State), pa.writer, nil); err != nil {
			return nil, fmt.Errorf(
				"deployment succeeded but the deployment result could not be displayed: %w",
				multierr.Combine(err, err),
			)
		}
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   "Your project has been provisioned!",
			FollowUp: getResourceGroupFollowUp(ctx, pa.formatter, pa.azCli, pa.projectConfig, pa.resourceManager, pa.env),
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

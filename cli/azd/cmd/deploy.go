// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/spin"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func deployCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := commands.Build(
		&deployAction{rootOptions: rootOptions},
		rootOptions,
		"deploy",
		"Deploy the application's code to Azure.",
		`Deploy the application's code to Azure.

When no `+withBackticks("--service")+` value is specified, all services in the *azure.yaml* file (found in the root of your project) are deployed.

Examples:

	$ azd deploy
	$ azd deploy –-service api
	$ azd deploy –-service web
	
After the deployment is complete, the endpoint is printed. To start the service, select the endpoint or paste it in a browser.`,
	)

	return output.AddOutputParam(
		cmd,
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat)
}

type deployAction struct {
	serviceName string
	rootOptions *commands.GlobalCommandOptions
}

type DeploymentResult struct {
	Timestamp time.Time                         `json:"timestamp"`
	Services  []project.ServiceDeploymentResult `json:"services"`
}

func (d *deployAction) SetupFlags(
	persis *pflag.FlagSet,
	local *pflag.FlagSet,
) {
	local.StringVar(&d.serviceName, "service", "", "Deploys a specific service (when the string is unspecified, all services that are listed in the "+environment.ProjectFileName+" file are deployed).")
}

func (d *deployAction) Run(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *environment.AzdContext) error {
	azCli := commands.GetAzCliFromContext(ctx)
	askOne := makeAskOne(d.rootOptions.NoPrompt)

	if err := ensureProject(azdCtx.ProjectPath()); err != nil {
		return err
	}

	if err := tools.EnsureInstalled(ctx, azCli); err != nil {
		return err
	}

	if err := ensureLoggedIn(ctx); err != nil {
		return fmt.Errorf("failed to ensure login: %w", err)
	}

	env, err := loadOrInitEnvironment(ctx, &d.rootOptions.EnvironmentName, azdCtx, askOne)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	projConfig, err := project.LoadProjectConfig(azdCtx.ProjectPath(), &env)
	if err != nil {
		return fmt.Errorf("loading project: %w", err)
	}

	if d.serviceName != "" && !projConfig.HasService(d.serviceName) {
		return fmt.Errorf("service name '%s' doesn't exist", d.serviceName)
	}

	proj, err := projConfig.GetProject(ctx, &env)
	if err != nil {
		return fmt.Errorf("creating project: %w", err)
	}

	// Collect all the tools we will need to do the deployment and validate that
	// the are installed. When a single project is being deployed, we need just
	// the tools for that project, otherwise we need the tools from all project.
	var allTools []tools.ExternalTool
	for _, svc := range proj.Services {
		if d.serviceName == "" || d.serviceName == svc.Config.Name {
			allTools = append(allTools, svc.RequiredExternalTools()...)
		}
	}

	if err := tools.EnsureInstalled(ctx, tools.Unique(allTools)...); err != nil {
		return err
	}

	formatter, err := output.GetFormatter(cmd)
	if err != nil {
		return err
	}
	interactive := formatter.Kind() == output.NoneFormat

	var svcDeploymentResult project.ServiceDeploymentResult
	var deploymentResults []project.ServiceDeploymentResult

	for _, svc := range proj.Services {
		// Skip this service if both cases are true:
		// 1. The user specified a service name
		// 2. This service is not the one the user specified
		if d.serviceName != "" && svc.Config.Name != d.serviceName {
			continue
		}

		deployAndReportProgress := func(showProgress func(string)) error {
			result, progress := svc.Deploy(ctx, azdCtx)

			// Report any progress
			go func() {
				for message := range progress {
					showProgress(fmt.Sprintf("- %s...", message))
				}
			}()

			response := <-result
			if response.Error != nil {
				return fmt.Errorf("deploying service: %w", response.Error)
			}

			svcDeploymentResult = *response.Result
			deploymentResults = append(deploymentResults, svcDeploymentResult)

			return nil
		}

		if interactive {
			deployMsg := fmt.Sprintf("Deploying service %s", svc.Config.Name)
			fmt.Println(deployMsg)
			spinner := spin.NewSpinner(deployMsg)
			spinner.Start()
			err = deployAndReportProgress(spinner.Title)
			spinner.Stop()

			if err == nil {
				reportServiceDeploymentResultInteractive(svc, &svcDeploymentResult)
			}
		} else {
			err = deployAndReportProgress(nil)
		}
		if err != nil {
			return err
		}
	}

	if formatter.Kind() == output.JsonFormat {
		aggregateDeploymentResult := DeploymentResult{
			Timestamp: time.Now(),
			Services:  deploymentResults,
		}

		if fmtErr := formatter.Format(aggregateDeploymentResult, cmd.OutOrStdout(), nil); fmtErr != nil {
			return fmt.Errorf("deployment result could not be displayed: %w", fmtErr)
		}
	}

	resourceGroups, err := azureutil.GetResourceGroupsForDeployment(ctx, azCli, env.GetSubscriptionId(), env.GetEnvName())
	if err != nil {
		return fmt.Errorf("discovering resource groups from deployment: %w", err)
	}

	for _, resourceGroup := range resourceGroups {
		resourcesGroupsURL := withLinkFormat(
			"https://portal.azure.com/#@/resource/subscriptions/%s/resourceGroups/%s/overview",
			env.GetSubscriptionId(),
			resourceGroup)
		printWithStyling(
			"View the resources created under the resource group %s in Azure Portal:\n%s\n",
			withHighLightFormat(resourceGroup),
			resourcesGroupsURL)
	}

	return nil
}

func reportServiceDeploymentResultInteractive(svc *project.Service, sdr *project.ServiceDeploymentResult) {
	var builder strings.Builder

	builder.WriteString(fmt.Sprintf("Deployed service %s\n", svc.Config.Name))

	for _, endpoint := range sdr.Endpoints {
		builder.WriteString(fmt.Sprintf(" - Endpoint: %s\n", withLinkFormat(endpoint)))
	}

	printWithStyling(builder.String())
	fmt.Println()
}

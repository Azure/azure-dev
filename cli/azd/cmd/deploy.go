// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type deployFlags struct {
	serviceName string
	global      *internal.GlobalCommandOptions
	*envFlag
}

func (d *deployFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	d.bindNonCommon(local, global)
	d.bindCommon(local, global)
}

func (d *deployFlags) bindNonCommon(
	local *pflag.FlagSet,
	global *internal.GlobalCommandOptions) {
	local.StringVar(
		&d.serviceName,
		"service",
		"",
		//nolint:lll
		"Deploys a specific service (when the string is unspecified, all services that are listed in the "+azdcontext.ProjectFileName+" file are deployed).",
	)
	d.global = global
}

func (d *deployFlags) bindCommon(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	d.envFlag = &envFlag{}
	d.envFlag.Bind(local, global)
}

func (d *deployFlags) setCommon(envFlag *envFlag) {
	d.envFlag = envFlag
}

func newDeployFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *deployFlags {
	flags := &deployFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newDeployCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deploy",
		Short: "Deploy the application's code to Azure.",
	}
}

type deployAction struct {
	flags           *deployFlags
	projectConfig   *project.ProjectConfig
	azdCtx          *azdcontext.AzdContext
	env             *environment.Environment
	projectManager  project.ProjectManager
	serviceManager  project.ServiceManager
	resourceManager project.ResourceManager
	accountManager  account.Manager
	azCli           azcli.AzCli
	formatter       output.Formatter
	writer          io.Writer
	console         input.Console
	commandRunner   exec.CommandRunner
}

func newDeployAction(
	flags *deployFlags,
	projectConfig *project.ProjectConfig,
	projectManager project.ProjectManager,
	serviceManager project.ServiceManager,
	resourceManager project.ResourceManager,
	azdCtx *azdcontext.AzdContext,
	environment *environment.Environment,
	accountManager account.Manager,
	azCli azcli.AzCli,
	commandRunner exec.CommandRunner,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
) actions.Action {
	return &deployAction{
		flags:           flags,
		projectConfig:   projectConfig,
		azdCtx:          azdCtx,
		env:             environment,
		projectManager:  projectManager,
		serviceManager:  serviceManager,
		resourceManager: resourceManager,
		accountManager:  accountManager,
		azCli:           azCli,
		formatter:       formatter,
		writer:          writer,
		console:         console,
		commandRunner:   commandRunner,
	}
}

type DeploymentResult struct {
	Timestamp time.Time                      `json:"timestamp"`
	Services  []*project.ServiceDeployResult `json:"services"`
}

func (d *deployAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Collect all the tools we will need to do the deployment and validate that
	// the are installed. When a single project is being deployed, we need just
	// the tools for that project, otherwise we need the tools from all project.
	var allTools []tools.ExternalTool
	for _, svc := range d.projectConfig.Services {
		if d.flags.serviceName == "" || d.flags.serviceName == svc.Name {
			serviceTools, err := d.serviceManager.GetRequiredTools(ctx, svc)
			if err != nil {
				return nil, fmt.Errorf("failed getting required tools for service %s: %w", svc.Name, err)
			}
			allTools = append(allTools, serviceTools...)
		}
	}

	if err := tools.EnsureInstalled(ctx, tools.Unique(allTools)...); err != nil {
		return nil, err
	}

	// Command title
	d.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Deploying services (azd deploy)",
	})

	var svcDeploymentResult *project.ServiceDeployResult
	var deploymentResults []*project.ServiceDeployResult

	for _, svc := range d.projectConfig.Services {
		// Skip this service if both cases are true:
		// 1. The user specified a service name
		// 2. This service is not the one the user specified
		if d.flags.serviceName != "" && svc.Name != d.flags.serviceName {
			continue
		}

		stepMessage := fmt.Sprintf("Deploying service %s", svc.Name)
		d.console.ShowSpinner(ctx, stepMessage, input.Step)

		deployTask := d.serviceManager.Deploy(ctx, svc)

		go func() {
			for progress := range deployTask.Progress() {
				updatedMessage := fmt.Sprintf("Deploying service %s (%s)", svc.Name, progress.Message)
				d.console.ShowSpinner(ctx, updatedMessage, input.Step)
			}
		}()

		result, err := deployTask.Await()

		d.console.StopSpinner(ctx, stepMessage, input.GetStepResultFormat(err))
		if err != nil {
			return nil, fmt.Errorf("deploying service: %w", err)
		}

		svcDeploymentResult = result
		deploymentResults = append(deploymentResults, svcDeploymentResult)

		// report endpoint
		for _, endpoint := range svcDeploymentResult.Endpoints {
			d.console.MessageUxItem(ctx, &ux.Endpoint{Endpoint: endpoint})
		}
	}

	if d.formatter.Kind() == output.JsonFormat {
		aggregateDeploymentResult := DeploymentResult{
			Timestamp: time.Now(),
			Services:  deploymentResults,
		}

		if fmtErr := d.formatter.Format(aggregateDeploymentResult, d.writer, nil); fmtErr != nil {
			return nil, fmt.Errorf("deployment result could not be displayed: %w", fmtErr)
		}
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   "Your Azure app has been deployed!",
			FollowUp: getResourceGroupFollowUp(ctx, d.formatter, d.azCli, d.projectConfig, d.resourceManager, d.env),
		},
	}, nil
}

func getCmdDeployHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription("Deploy application to Azure.", []string{
		formatHelpNote(fmt.Sprintf("When no %s value is specified, all services in the 'azure.yaml'"+
			" file (found in the root of your project) are deployed.", output.WithHighLightFormat("--service"))),
		formatHelpNote("After the deployment is complete, the endpoint is printed. To start the service, select" +
			" the endpoint or paste it in a browser."),
	})
}

func getCmdDeployHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Reviews all code and services in your azure.yaml file and deploys to Azure.": output.WithHighLightFormat(
			"azd deploy"),
		"Deploy all application API services to Azure.": output.WithHighLightFormat("azd deploy --service api"),
		"Deploy all application web services to Azure.": output.WithHighLightFormat("azd deploy --service web"),
	})
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
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
	local.StringVar(
		&d.serviceName,
		"service",
		"",
		//nolint:lll
		"Deploys a specific service (when the string is unspecified, all services that are listed in the "+azdcontext.ProjectFileName+" file are deployed).",
	)
	//deprecate:flag hide --service
	_ = local.MarkHidden("service")
}

func newDeployFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *deployFlags {
	flags := &deployFlags{
		envFlag: newEnvFlag(cmd, global),
		global:  global,
	}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newDeployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy <service>",
		Short: "Deploy the application's code to Azure.",
	}
	cmd.Args = cobra.MaximumNArgs(1)

	return cmd
}

type deployAction struct {
	flags                    *deployFlags
	args                     []string
	projectConfig            *project.ProjectConfig
	azdCtx                   *azdcontext.AzdContext
	env                      *environment.Environment
	projectManager           project.ProjectManager
	serviceManager           project.ServiceManager
	resourceManager          project.ResourceManager
	accountManager           account.Manager
	azCli                    azcli.AzCli
	formatter                output.Formatter
	writer                   io.Writer
	console                  input.Console
	commandRunner            exec.CommandRunner
	middlewareRunner         middleware.MiddlewareContext
	packageActionInitializer actions.ActionInitializer[*packageAction]
}

func newDeployAction(
	flags *deployFlags,
	args []string,
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
	middlewareRunner middleware.MiddlewareContext,
	packageActionInitializer actions.ActionInitializer[*packageAction],
) actions.Action {
	return &deployAction{
		flags:                    flags,
		args:                     args,
		projectConfig:            projectConfig,
		azdCtx:                   azdCtx,
		env:                      environment,
		projectManager:           projectManager,
		serviceManager:           serviceManager,
		resourceManager:          resourceManager,
		accountManager:           accountManager,
		azCli:                    azCli,
		formatter:                formatter,
		writer:                   writer,
		console:                  console,
		commandRunner:            commandRunner,
		middlewareRunner:         middlewareRunner,
		packageActionInitializer: packageActionInitializer,
	}
}

type DeploymentResult struct {
	Timestamp time.Time                                `json:"timestamp"`
	Services  map[string]*project.ServicePublishResult `json:"services"`
}

func (da *deployAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	targetServiceName := da.flags.serviceName
	if len(da.args) == 1 {
		targetServiceName = da.args[0]
	}

	packageAction, err := da.packageActionInitializer()
	packageAction.args = da.args
	packageAction.flags.serviceName = da.flags.serviceName
	if err != nil {
		return nil, err
	}

	packageOptions := &middleware.Options{CommandPath: "package"}
	_, err = da.middlewareRunner.RunChildAction(ctx, packageOptions, packageAction)
	if err != nil {
		return nil, err
	}

	// Command title
	da.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Deploying services (azd deploy)",
	})

	serviceNameWarningCheck(da.console, da.flags.serviceName, "deploy")

	if targetServiceName != "" && !da.projectConfig.HasService(targetServiceName) {
		return nil, fmt.Errorf("service name '%s' doesn't exist", targetServiceName)
	}

	if err := da.ensureTools(ctx, targetServiceName); err != nil {
		return nil, err
	}

	if err := da.projectManager.Initialize(ctx, da.projectConfig); err != nil {
		return nil, err
	}

	publishResults := map[string]*project.ServicePublishResult{}

	for _, svc := range da.projectConfig.Services {
		stepMessage := fmt.Sprintf("Deploying service %s", svc.Name)
		da.console.ShowSpinner(ctx, stepMessage, input.Step)

		// Skip this service if both cases are true:
		// 1. The user specified a service name
		// 2. This service is not the one the user specified
		if targetServiceName != "" && targetServiceName != svc.Name {
			da.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
			continue
		}

		publishTask := da.serviceManager.Publish(ctx, svc, nil)
		go func() {
			for publishProgress := range publishTask.Progress() {
				progressMessage := fmt.Sprintf("Deploying service %s (%s)", svc.Name, publishProgress.Message)
				da.console.ShowSpinner(ctx, progressMessage, input.Step)
			}
		}()

		publishResult, err := publishTask.Await()
		if err != nil {
			da.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, err
		}

		da.console.StopSpinner(ctx, stepMessage, input.StepDone)
		publishResults[svc.Name] = publishResult

		// report build outputs
		da.console.MessageUxItem(ctx, publishResult)
	}

	if da.formatter.Kind() == output.JsonFormat {
		deployResult := DeploymentResult{
			Timestamp: time.Now(),
			Services:  publishResults,
		}

		if fmtErr := da.formatter.Format(deployResult, da.writer, nil); fmtErr != nil {
			return nil, fmt.Errorf("deploy result could not be displayed: %w", fmtErr)
		}
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   "Your Azure app has been deployed!",
			FollowUp: getResourceGroupFollowUp(ctx, da.formatter, da.azCli, da.projectConfig, da.resourceManager, da.env),
		},
	}, nil
}

func (d *deployAction) ensureTools(ctx context.Context, targetServiceName string) error {
	// Collect all the tools we will need to do the deployment and validate that
	// the are installed. When a single project is being deployed, we need just
	// the tools for that project, otherwise we need the tools from all project.
	var allTools []tools.ExternalTool
	for _, svc := range d.projectConfig.Services {
		if targetServiceName == "" || targetServiceName == svc.Name {
			serviceTarget, err := d.serviceManager.GetServiceTarget(ctx, svc)
			if err != nil {
				return err
			}

			serviceTools := serviceTarget.RequiredExternalTools(ctx)
			allTools = append(allTools, serviceTools...)
		}
	}

	if err := tools.EnsureInstalled(ctx, tools.Unique(allTools)...); err != nil {
		return fmt.Errorf("failed getting required tools for project, %w", err)
	}

	return nil
}

func getCmdDeployHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription("Deploy application to Azure.", []string{
		formatHelpNote(fmt.Sprintf("When %s is not set, all services in the 'azure.yaml'"+
			" file (found in the root of your project) are deployed.", output.WithHighLightFormat("<service>"))),
		formatHelpNote("After the deployment is complete, the endpoint is printed. To start the service, select" +
			" the endpoint or paste it in a browser."),
	})
}

func getCmdDeployHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Reviews all code and services in your azure.yaml file and deploys to Azure.": output.WithHighLightFormat(
			"azd deploy"),
		"Deploy all application API services to Azure.": output.WithHighLightFormat("azd deploy api"),
		"Deploy all application web services to Azure.": output.WithHighLightFormat("azd deploy web"),
	})
}

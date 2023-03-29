// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
	all         bool
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
	local.BoolVar(
		&d.all,
		"all",
		false,
		"Deploys all services that are listed in "+azdcontext.ProjectFileName,
	)
	local.StringVar(
		&d.serviceName,
		"service",
		"",
		//nolint:lll
		"Deploys a specific service (when the string is unspecified, all services that are listed in the "+azdcontext.ProjectFileName+" file are deployed).",
	)
	//deprecate:flag hide --service
	_ = local.MarkHidden("service")
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
	cmd := &cobra.Command{
		Use:   "deploy <service>",
		Short: "Deploy the application's code to Azure.",
	}
	cmd.Args = cobra.MaximumNArgs(1)

	return cmd
}

type deployAction struct {
	flags           *deployFlags
	args            []string
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
) actions.Action {
	return &deployAction{
		flags:           flags,
		args:            args,
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
	if d.flags.serviceName != "" {
		fmt.Println(
			d.console.Handles().Stderr,
			//nolint:Lll
			output.WithWarningFormat("--service flag is no longer required. Simply run azd deploy <service> instead."))
	}

	targetServiceName := d.flags.serviceName
	if len(d.args) == 1 {
		targetServiceName = d.args[0]
	}

	if targetServiceName != "" && !d.projectConfig.HasService(targetServiceName) {
		return nil, fmt.Errorf("service name '%s' doesn't exist", targetServiceName)
	}

	if d.flags.all && targetServiceName != "" {
		return nil, fmt.Errorf("cannot specify both --all and <service>")
	}

	if !d.flags.all && targetServiceName == "" {
		var err error
		targetServiceName, err = defaultServiceFromWd(d.azdCtx, d.projectConfig)
		if err == errNoDefaultService {
			return nil, fmt.Errorf(
				//nolint:lll
				"current working directory is not a project or service directory. Please specify a service name to deploy a service, or use --all to deploy all services")
		} else if err != nil {
			return nil, err
		}
	}

	if err := d.projectManager.Initialize(ctx, d.projectConfig); err != nil {
		return nil, err
	}

	services := d.projectConfig.GetServices()
	targetServices := make([]*project.ServiceConfig, 0, len(services))
	for _, svc := range services {
		// If targetServiceName is empty (which is only allowed if --all is set),
		// add all services.
		// If service is specified, add the matching service.
		if targetServiceName == "" || targetServiceName == svc.Name {
			targetServices = append(targetServices, svc)
		}
	}

	// Collect all the tools we will need to do the deployment and validate that
	// the are installed. When a single project is being deployed, we need just
	// the tools for that project, otherwise we need the tools from all project.
	var allTools []tools.ExternalTool
	for _, svc := range targetServices {
		serviceTools, err := d.serviceManager.GetRequiredTools(ctx, svc)
		if err != nil {
			return nil, fmt.Errorf("failed getting required tools for service %s: %w", svc.Name, err)
		}
		allTools = append(allTools, serviceTools...)
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

	for _, svc := range targetServices {
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
		for _, endpoint := range svcDeploymentResult.Publish.Endpoints {
			d.console.MessageUxItem(ctx, &ux.Endpoint{Endpoint: endpoint})
		}
	}

	if targetServiceName != "" && len(deploymentResults) == 0 {
		return nil, fmt.Errorf("no services were deployed. Check the specified service name and try again.")
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

var errNoDefaultService = errors.New("no default service selection matches the working directory")

// Returns the default service name to target based on the current working directory.
//
//   - If the working directory is the project directory, then an empty string is returned to indicate all services.
//   - If the working directory is a service directory, then the name of the service is returned.
//   - If the working directory is neither the project directory nor a service directory, then
//     errNoDefaultService is returned.
func defaultServiceFromWd(
	azdCtx *azdcontext.AzdContext,
	projConfig *project.ProjectConfig) (targetService string, err error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	if wd == azdCtx.ProjectDirectory() {
		return "", nil
	} else {
		for _, svcConfig := range projConfig.Services {
			if wd == svcConfig.Path() {
				return svcConfig.Name, nil
			}
		}

		return "", errNoDefaultService
	}
}

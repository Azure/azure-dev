// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type DeployFlags struct {
	serviceName string
	All         bool
	fromPackage string
	global      *internal.GlobalCommandOptions
	*internal.EnvFlag
}

func (d *DeployFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	d.BindNonCommon(local, global)
	d.bindCommon(local, global)
}

func (d *DeployFlags) BindNonCommon(
	local *pflag.FlagSet,
	global *internal.GlobalCommandOptions) {
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

func (d *DeployFlags) bindCommon(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	d.EnvFlag = &internal.EnvFlag{}
	d.EnvFlag.Bind(local, global)

	local.BoolVar(
		&d.All,
		"all",
		false,
		"Deploys all services that are listed in "+azdcontext.ProjectFileName,
	)
	local.StringVar(
		&d.fromPackage,
		"from-package",
		"",
		//nolint:lll
		"Deploys the packaged service located at the provided path. Supports zipped file packages (file path) or container images (image tag).",
	)
}

func (d *DeployFlags) SetCommon(envFlag *internal.EnvFlag) {
	d.EnvFlag = envFlag
}

func NewDeployFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *DeployFlags {
	flags := &DeployFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func NewDeployFlagsFromEnvAndOptions(envFlag *internal.EnvFlag, global *internal.GlobalCommandOptions) *DeployFlags {
	return &DeployFlags{
		EnvFlag: envFlag,
		global:  global,
	}
}

func NewDeployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy <service>",
		Short: "Deploy the application's code to Azure.",
	}
	cmd.Args = cobra.MaximumNArgs(1)

	return cmd
}

type DeployAction struct {
	flags               *DeployFlags
	args                []string
	projectConfig       *project.ProjectConfig
	azdCtx              *azdcontext.AzdContext
	env                 *environment.Environment
	projectManager      project.ProjectManager
	serviceManager      project.ServiceManager
	resourceManager     project.ResourceManager
	accountManager      account.Manager
	azCli               *azapi.AzureClient
	portalUrlBase       string
	formatter           output.Formatter
	writer              io.Writer
	console             input.Console
	commandRunner       exec.CommandRunner
	alphaFeatureManager *alpha.FeatureManager
	importManager       *project.ImportManager
}

func NewDeployAction(
	flags *DeployFlags,
	args []string,
	projectConfig *project.ProjectConfig,
	projectManager project.ProjectManager,
	serviceManager project.ServiceManager,
	resourceManager project.ResourceManager,
	azdCtx *azdcontext.AzdContext,
	environment *environment.Environment,
	accountManager account.Manager,
	cloud *cloud.Cloud,
	azCli *azapi.AzureClient,
	commandRunner exec.CommandRunner,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	alphaFeatureManager *alpha.FeatureManager,
	importManager *project.ImportManager,
) actions.Action {
	return &DeployAction{
		flags:               flags,
		args:                args,
		projectConfig:       projectConfig,
		azdCtx:              azdCtx,
		env:                 environment,
		projectManager:      projectManager,
		serviceManager:      serviceManager,
		resourceManager:     resourceManager,
		accountManager:      accountManager,
		portalUrlBase:       cloud.PortalUrlBase,
		azCli:               azCli,
		formatter:           formatter,
		writer:              writer,
		console:             console,
		commandRunner:       commandRunner,
		alphaFeatureManager: alphaFeatureManager,
		importManager:       importManager,
	}
}

type DeploymentResult struct {
	Timestamp time.Time                               `json:"timestamp"`
	Services  map[string]*project.ServiceDeployResult `json:"services"`
}

func (da *DeployAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	targetServiceName := da.flags.serviceName
	if len(da.args) == 1 {
		targetServiceName = da.args[0]
	}

	serviceNameWarningCheck(da.console, da.flags.serviceName, "deploy")

	if da.env.GetSubscriptionId() == "" {
		return nil, errors.New(
			"infrastructure has not been provisioned. Run `azd provision`",
		)
	}

	targetServiceName, err := getTargetServiceName(
		ctx,
		da.projectManager,
		da.importManager,
		da.projectConfig,
		string(project.ServiceEventDeploy),
		targetServiceName,
		da.flags.All,
	)
	if err != nil {
		return nil, err
	}

	if da.flags.All && da.flags.fromPackage != "" {
		return nil, errors.New(
			"'--from-package' cannot be specified when '--all' is set. Specify a specific service by passing a <service>")
	}

	if targetServiceName == "" && da.flags.fromPackage != "" {
		return nil, errors.New(
			//nolint:lll
			"'--from-package' cannot be specified when deploying all services. Specify a specific service by passing a <service>",
		)
	}

	if err := da.projectManager.Initialize(ctx, da.projectConfig); err != nil {
		return nil, err
	}

	if err := da.projectManager.EnsureServiceTargetTools(ctx, da.projectConfig, func(svc *project.ServiceConfig) bool {
		return targetServiceName == "" || svc.Name == targetServiceName
	}); err != nil {
		return nil, err
	}

	// Command title
	da.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Deploying services (azd deploy)",
	})

	startTime := time.Now()

	deployResults := map[string]*project.ServiceDeployResult{}
	stableServices, err := da.importManager.ServiceStable(ctx, da.projectConfig)
	if err != nil {
		return nil, err
	}

	for _, svc := range stableServices {
		stepMessage := fmt.Sprintf("Deploying service %s", svc.Name)
		da.console.ShowSpinner(ctx, stepMessage, input.Step)

		// Skip this service if both cases are true:
		// 1. The user specified a service name
		// 2. This service is not the one the user specified
		if targetServiceName != "" && targetServiceName != svc.Name {
			da.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
			continue
		}

		if alphaFeatureId, isAlphaFeature := alpha.IsFeatureKey(string(svc.Host)); isAlphaFeature {
			// alpha feature on/off detection for host is done during initialization.
			// This is just for displaying the warning during deployment.
			da.console.WarnForFeature(ctx, alphaFeatureId)
		}

		var packageResult *project.ServicePackageResult
		if da.flags.fromPackage != "" {
			// --from-package set, skip packaging
			packageResult = &project.ServicePackageResult{
				PackagePath: da.flags.fromPackage,
			}
		} else {
			//  --from-package not set, package the application
			packageResult, err = async.RunWithProgress(
				func(packageProgress project.ServiceProgress) {
					progressMessage := fmt.Sprintf("Deploying service %s (%s)", svc.Name, packageProgress.Message)
					da.console.ShowSpinner(ctx, progressMessage, input.Step)
				},
				func(progress *async.Progress[project.ServiceProgress]) (*project.ServicePackageResult, error) {
					return da.serviceManager.Package(ctx, svc, nil, progress, nil)
				},
			)

			// do not stop progress here as next step is to deploy
			if err != nil {
				da.console.StopSpinner(ctx, stepMessage, input.StepFailed)
				return nil, err
			}
		}

		deployResult, err := async.RunWithProgress(
			func(deployProgress project.ServiceProgress) {
				progressMessage := fmt.Sprintf("Deploying service %s (%s)", svc.Name, deployProgress.Message)
				da.console.ShowSpinner(ctx, progressMessage, input.Step)
			},
			func(progress *async.Progress[project.ServiceProgress]) (*project.ServiceDeployResult, error) {
				return da.serviceManager.Deploy(ctx, svc, packageResult, progress)
			},
		)

		da.console.StopSpinner(ctx, stepMessage, input.GetStepResultFormat(err))
		if err != nil {
			return nil, err
		}

		deployResults[svc.Name] = deployResult

		// report deploy outputs
		da.console.MessageUxItem(ctx, deployResult)
	}

	aspireDashboardUrl := apphost.AspireDashboardUrl(ctx, da.env, da.alphaFeatureManager)
	if aspireDashboardUrl != nil {
		da.console.MessageUxItem(ctx, aspireDashboardUrl)
	}

	if da.formatter.Kind() == output.JsonFormat {
		deployResult := DeploymentResult{
			Timestamp: time.Now(),
			Services:  deployResults,
		}

		if fmtErr := da.formatter.Format(deployResult, da.writer, nil); fmtErr != nil {
			return nil, fmt.Errorf("deploy result could not be displayed: %w", fmtErr)
		}
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf("Your application was deployed to Azure in %s.", ux.DurationAsText(since(startTime))),
			FollowUp: getResourceGroupFollowUp(ctx,
				da.formatter,
				da.portalUrlBase,
				da.projectConfig,
				da.resourceManager,
				da.env,
				false,
			),
		},
	}, nil
}

func GetCmdDeployHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription("Deploy application to Azure.", []string{
		formatHelpNote(
			"By default, deploys all services listed in 'azure.yaml' in the current directory," +
				" or the service described in the project that matches the current directory."),
		formatHelpNote(
			fmt.Sprintf("When %s is set, only the specific service is deployed.", output.WithHighLightFormat("<service>"))),
		formatHelpNote("After the deployment is complete, the endpoint is printed. To start the service, select" +
			" the endpoint or paste it in a browser."),
	})
}

func GetCmdDeployHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Deploy all services in the current project to Azure.": output.WithHighLightFormat(
			"azd deploy --all",
		),
		"Deploy the service named 'api' to Azure.": output.WithHighLightFormat(
			"azd deploy api",
		),
		"Deploy the service named 'web' to Azure.": output.WithHighLightFormat(
			"azd deploy web",
		),
		"Deploy the service named 'api' to Azure from a previously generated package.": output.WithHighLightFormat(
			"azd deploy api --from-package <package-path>",
		),
	})
}

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
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/operations"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type deployFlags struct {
	serviceName string
	all         bool
	fromPackage string
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
	//deprecate:flag hide --service
	_ = local.MarkHidden("service")
	d.global = global
}

func (d *deployFlags) bindCommon(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	d.envFlag = &envFlag{}
	d.envFlag.Bind(local, global)

	local.BoolVar(
		&d.all,
		"all",
		false,
		"Deploys all services that are listed in "+azdcontext.ProjectFileName,
	)
	local.StringVar(
		&d.fromPackage,
		"from-package",
		"",
		"Deploys the application from an existing package.",
	)
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
	alphaFeatureManager      *alpha.FeatureManager
	operationManager         operations.Manager
	operationPrinter         operations.Printer
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
	alphaFeatureManager *alpha.FeatureManager,
	operationManager operations.Manager,
	operationPrinter operations.Printer,
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
		alphaFeatureManager:      alphaFeatureManager,
		operationManager:         operationManager,
		operationPrinter:         operationPrinter,
	}
}

type DeploymentResult struct {
	Timestamp time.Time                               `json:"timestamp"`
	Services  map[string]*project.ServiceDeployResult `json:"services"`
}

func (da *deployAction) Run(ctx context.Context) (*actions.ActionResult, error) {
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
		da.projectConfig,
		string(project.ServiceEventDeploy),
		targetServiceName,
		da.flags.all,
	)
	if err != nil {
		return nil, err
	}

	if da.flags.all && da.flags.fromPackage != "" {
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

	for _, svc := range da.projectConfig.GetServicesStable() {
		var deployResult *project.ServiceDeployResult
		operationMessage := fmt.Sprintf("Deploying service %s", svc.Name)
		err = da.operationManager.Run(ctx, operationMessage, func(operation *operations.Operation) error {
			// Skip this service if both cases are true:
			// 1. The user specified a service name
			// 2. This service is not the one the user specified
			if targetServiceName != "" && targetServiceName != svc.Name {
				operation.Skip(ctx)
				return nil
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
				packageTask := da.serviceManager.Package(ctx, svc, nil)
				go func() {
					for packageProgress := range packageTask.Progress() {
						da.operationManager.ReportProgress(ctx, packageProgress.Message)
					}
				}()

				packageResult, err = packageTask.Await()
				if err != nil {
					return err
				}
			}

			operationResult, err := da.serviceManager.Deploy(ctx, svc, packageResult)
			if err != nil {
				return err
			}

			deployResult = operationResult
			return nil
		})

		if err != nil {
			return nil, err
		}

		if deployResult != nil {
			deployResults[svc.Name] = deployResult

			// report deploy outputs
			da.console.MessageUxItem(ctx, deployResult)
		}
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

	if err := da.operationPrinter.Flush(ctx); err != nil {
		return nil, err
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   fmt.Sprintf("Your application was deployed to Azure in %s.", ux.DurationAsText(since(startTime))),
			FollowUp: getResourceGroupFollowUp(ctx, da.formatter, da.projectConfig, da.resourceManager, da.env),
		},
	}, nil
}

func getCmdDeployHelpDescription(*cobra.Command) string {
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

func getCmdDeployHelpFooter(*cobra.Command) string {
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

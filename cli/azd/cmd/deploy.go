// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
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
	serviceName  string
	outputFormat *string // pointer to allow delay-initialization when used in "azd up"
	global       *internal.GlobalCommandOptions
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

	d.outputFormat = convert.RefOf("")
	output.AddOutputFlag(
		local,
		d.outputFormat,
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat)
}

func (d *deployFlags) setCommon(outputFormat *string, envFlag *envFlag) {
	d.outputFormat = outputFormat
	d.envFlag = envFlag
}

func deployCmdDesign(rootOptions *internal.GlobalCommandOptions) (*cobra.Command, *deployFlags) {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy the application's code to Azure.",
		//nolint:lll
		Long: `Deploy the application's code to Azure.
When no ` + output.WithBackticks("--service") + ` value is specified, all services in the ` + output.WithBackticks("azure.yaml") + ` file (found in the root of your project) are deployed.

Examples:

	$ azd deploy
	$ azd deploy --service api
	$ azd deploy --service web
	
After the deployment is complete, the endpoint is printed. To start the service, select the endpoint or paste it in a browser.`,
	}
	df := deployFlags{}
	df.Bind(cmd.Flags(), rootOptions)

	return cmd, &df
}

type deployAction struct {
	flags         deployFlags
	azCli         azcli.AzCli
	azdCtx        *azdcontext.AzdContext
	formatter     output.Formatter
	writer        io.Writer
	console       input.Console
	commandRunner exec.CommandRunner
}

func newDeployAction(
	flags deployFlags,
	azCli azcli.AzCli,
	commandRunner exec.CommandRunner,
	azdCtx *azdcontext.AzdContext,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
) (*deployAction, error) {
	da := &deployAction{
		flags:         flags,
		azCli:         azCli,
		azdCtx:        azdCtx,
		formatter:     formatter,
		writer:        writer,
		console:       console,
		commandRunner: commandRunner,
	}

	return da, nil
}

type DeploymentResult struct {
	Timestamp time.Time                         `json:"timestamp"`
	Services  []project.ServiceDeploymentResult `json:"services"`
}

func (d *deployAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	env, err := loadOrInitEnvironment(ctx, &d.flags.environmentName, d.azdCtx, d.console, d.azCli)
	if err != nil {
		return nil, fmt.Errorf("loading environment: %w", err)
	}

	projConfig, err := project.LoadProjectConfig(d.azdCtx.ProjectPath())
	if err != nil {
		return nil, fmt.Errorf("loading project: %w", err)
	}

	if d.flags.serviceName != "" && !projConfig.HasService(d.flags.serviceName) {
		return nil, fmt.Errorf("service name '%s' doesn't exist", d.flags.serviceName)
	}

	proj, err := projConfig.GetProject(ctx, env, d.console, d.azCli, d.commandRunner)
	if err != nil {
		return nil, fmt.Errorf("creating project: %w", err)
	}

	// Collect all the tools we will need to do the deployment and validate that
	// the are installed. When a single project is being deployed, we need just
	// the tools for that project, otherwise we need the tools from all project.
	var allTools []tools.ExternalTool
	for _, svc := range proj.Services {
		if d.flags.serviceName == "" || d.flags.serviceName == svc.Config.Name {
			allTools = append(allTools, svc.RequiredExternalTools()...)
		}
	}

	if err := tools.EnsureInstalled(ctx, tools.Unique(allTools)...); err != nil {
		return nil, err
	}

	// Command title
	d.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Deploying services (azd deploy)",
	})

	var svcDeploymentResult project.ServiceDeploymentResult
	var deploymentResults []project.ServiceDeploymentResult

	for _, svc := range proj.Services {
		// Skip this service if both cases are true:
		// 1. The user specified a service name
		// 2. This service is not the one the user specified
		if d.flags.serviceName != "" && svc.Config.Name != d.flags.serviceName {
			continue
		}

		stepMessage := fmt.Sprintf("Deploying service %s", svc.Config.Name)
		d.console.ShowSpinner(ctx, stepMessage, input.Step)
		result, progress := svc.Deploy(ctx, d.azdCtx)

		// Report any progress to logs only. Changes for the console are managed by the console object.
		// This routine is required to drain all the string messages sent by the `progress`.
		go func() {
			for message := range progress {
				log.Printf("- %s...", message)
			}
		}()

		// block until deploy thread returns
		response := <-result

		d.console.StopSpinner(ctx, stepMessage, input.GetStepResultFormat(err))
		if response.Error != nil {
			return nil, fmt.Errorf("deploying service: %w", response.Error)
		}

		svcDeploymentResult = *response.Result
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
			FollowUp: getResourceGroupFollowUp(ctx, d.formatter, d.azCli, projConfig, env),
		},
	}, nil
}

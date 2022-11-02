// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/spin"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type deployFlags struct {
	serviceName  string
	outputFormat *string // pointer to allow delay-initialization when used in "azd up"
	global       *internal.GlobalCommandOptions
}

func (d *deployFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	d.bindWithoutOutput(local, global)

	d.outputFormat = convert.RefOf("")
	output.AddOutputFlag(
		local,
		d.outputFormat,
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat)
}

// bindWithoutOutput binds all flags except for the output flag. This is used when multiple actions are attached
// to the same command.
func (d *deployFlags) bindWithoutOutput(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.StringVar(
		&d.serviceName,
		"service",
		"",
		//nolint:lll
		"Deploys a specific service (when the string is unspecified, all services that are listed in the "+azdcontext.ProjectFileName+" file are deployed).",
	)

	d.global = global
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
	flags     deployFlags
	azdCtx    *azdcontext.AzdContext
	formatter output.Formatter
	writer    io.Writer
	console   input.Console
}

func newDeployAction(
	flags deployFlags,
	azdCtx *azdcontext.AzdContext,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
) (*deployAction, error) {
	da := &deployAction{
		flags:     flags,
		azdCtx:    azdCtx,
		formatter: formatter,
		writer:    writer,
		console:   console,
	}

	return da, nil
}

type DeploymentResult struct {
	Timestamp time.Time                         `json:"timestamp"`
	Services  []project.ServiceDeploymentResult `json:"services"`
}

func (d *deployAction) Run(ctx context.Context) error {
	if err := ensureProject(d.azdCtx.ProjectPath()); err != nil {
		return err
	}

	if err := ensureLoggedIn(ctx); err != nil {
		return fmt.Errorf("failed to ensure login: %w", err)
	}

	env, ctx, err := loadOrInitEnvironment(ctx, &d.flags.global.EnvironmentName, d.azdCtx, d.console)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	projConfig, err := project.LoadProjectConfig(d.azdCtx.ProjectPath(), env)
	if err != nil {
		return fmt.Errorf("loading project: %w", err)
	}

	if d.flags.serviceName != "" && !projConfig.HasService(d.flags.serviceName) {
		return fmt.Errorf("service name '%s' doesn't exist", d.flags.serviceName)
	}

	proj, err := projConfig.GetProject(&ctx, env)
	if err != nil {
		return fmt.Errorf("creating project: %w", err)
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
		return err
	}

	interactive := d.formatter.Kind() == output.NoneFormat

	var svcDeploymentResult project.ServiceDeploymentResult
	var deploymentResults []project.ServiceDeploymentResult

	for _, svc := range proj.Services {
		// Skip this service if both cases are true:
		// 1. The user specified a service name
		// 2. This service is not the one the user specified
		if d.flags.serviceName != "" && svc.Config.Name != d.flags.serviceName {
			continue
		}

		deployAndReportProgress := func(ctx context.Context, showProgress func(string)) error {
			result, progress := svc.Deploy(ctx, d.azdCtx)

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
			deployMsg := fmt.Sprintf("Deploying service %s...", output.WithHighLightFormat(svc.Config.Name))
			d.console.Message(ctx, deployMsg)

			spinner, ctx := spin.GetOrCreateSpinner(ctx, d.console.Handles().Stdout, deployMsg)

			spinner.Start()
			err = deployAndReportProgress(ctx, spinner.Title)
			spinner.Stop()

			if err == nil {
				reportServiceDeploymentResultInteractive(ctx, d.console, svc, &svcDeploymentResult)
			}
		} else {
			err = deployAndReportProgress(ctx, nil)
		}
		if err != nil {
			return err
		}
	}

	if d.formatter.Kind() == output.JsonFormat {
		aggregateDeploymentResult := DeploymentResult{
			Timestamp: time.Now(),
			Services:  deploymentResults,
		}

		if fmtErr := d.formatter.Format(aggregateDeploymentResult, d.writer, nil); fmtErr != nil {
			return fmt.Errorf("deployment result could not be displayed: %w", fmtErr)
		}
	}

	return nil
}

func reportServiceDeploymentResultInteractive(
	ctx context.Context,
	console input.Console,
	svc *project.Service,
	sdr *project.ServiceDeploymentResult,
) {
	var builder strings.Builder

	builder.WriteString(fmt.Sprintf("Deployed service %s\n", output.WithHighLightFormat(svc.Config.Name)))

	for _, endpoint := range sdr.Endpoints {
		builder.WriteString(fmt.Sprintf(" - Endpoint: %s\n", output.WithLinkFormat(endpoint)))
	}

	console.Message(ctx, builder.String())
}

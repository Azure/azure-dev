package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"go.uber.org/multierr"
)

type provisionFlags struct {
	noProgress bool
	whatIf     bool
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
	local.BoolVar(&i.whatIf, "what-if", false, "Show the expected changes for the infrastructure without provisioning.")
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
	}
}

type provisionAction struct {
	flags            *provisionFlags
	provisionManager *provisioning.Manager
	projectManager   project.ProjectManager
	resourceManager  project.ResourceManager
	env              *environment.Environment
	formatter        output.Formatter
	projectConfig    *project.ProjectConfig
	writer           io.Writer
	console          input.Console
}

func newProvisionAction(
	flags *provisionFlags,
	provisionManager *provisioning.Manager,
	projectManager project.ProjectManager,
	resourceManager project.ResourceManager,
	projectConfig *project.ProjectConfig,
	env *environment.Environment,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
) actions.Action {
	return &provisionAction{
		flags:            flags,
		provisionManager: provisionManager,
		projectManager:   projectManager,
		resourceManager:  resourceManager,
		env:              env,
		formatter:        formatter,
		projectConfig:    projectConfig,
		writer:           writer,
		console:          console,
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
	whatIfMode := p.flags.whatIf

	// Command title
	defaultTitle := "Provisioning Azure resources (azd provision)"
	defaultTitleNote := "Provisioning Azure resources can take some time"
	if whatIfMode {
		defaultTitle = "Preview Azure resources changes (azd provision --what-if)"
		defaultTitleNote = "No changes will be persisted to your Azure subscription"
	}

	p.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     defaultTitle,
		TitleNote: defaultTitleNote},
	)

	startTime := time.Now()

	if err := p.projectManager.Initialize(ctx, p.projectConfig); err != nil {
		return nil, err
	}

	if err := p.provisionManager.Initialize(ctx, p.projectConfig.Path, p.projectConfig.Infra); err != nil {
		return nil, fmt.Errorf("initializing provisioning manager: %w", err)
	}

	var deployResult *provisioning.DeployResult
	var deployPreviewResult *provisioning.DeployPreviewResult

	projectEventArgs := project.ProjectLifecycleEventArgs{
		Project: p.projectConfig,
	}

	err := p.projectConfig.Invoke(ctx, project.ProjectEventProvision, projectEventArgs, func() error {
		deploymentPlan, err := p.provisionManager.Plan(ctx)
		if err != nil {
			return fmt.Errorf("planning deployment: %w", err)
		}

		if p.flags.whatIf {
			deployPreviewResult, err = p.provisionManager.WhatIfDeploy(ctx, deploymentPlan)
		} else {
			deployResult, err = p.provisionManager.Deploy(ctx, deploymentPlan)
		}

		return err
	})

	if err != nil {
		if p.formatter.Kind() == output.JsonFormat {
			stateResult, err := p.provisionManager.State(ctx)
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

	if p.flags.whatIf {

		p.console.MessageUxItem(ctx, deployResultToUx(deployPreviewResult))

		return &actions.ActionResult{
			Message: &actions.ResultMessage{
				Header: fmt.Sprintf(
					"Generated provisioning preview in %s.", ux.DurationAsText(since(startTime))),
				FollowUp: getResourceGroupFollowUp(
					ctx, p.formatter, p.projectConfig, p.resourceManager, p.env),
			},
		}, nil
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
		stateResult, err := p.provisionManager.State(ctx)
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
			Header: fmt.Sprintf(
				"Your application was provisioned in Azure in %s.", ux.DurationAsText(since(startTime))),
			FollowUp: getResourceGroupFollowUp(
				ctx, p.formatter, p.projectConfig, p.resourceManager, p.env),
		},
	}, nil
}

func deployResultToUx(previewResult *provisioning.DeployPreviewResult) ux.UxItem {
	var operations []*ux.Resource
	for _, change := range previewResult.Preview.Properties.Changes {
		operations = append(operations, &ux.Resource{
			Operation: ux.OperationType(change.ChangeType),
			Type:      change.ResourceType,
			Name:      change.Name,
		})
	}
	return &ux.PreviewProvision{
		Operations: operations,
	}
}

func getCmdProvisionHelpDescription(c *cobra.Command) string {
	return generateCmdHelpDescription(fmt.Sprintf(
		"Provision the Azure resources for an application."+
			" This step may take a while depending on the resources provisioned."+
			" You should run %s any time you update your Bicep or Terraform file."+
			"\n\nThis command prompts you to input the following:",
		output.WithHighLightFormat(c.CommandPath())), []string{
		formatHelpNote("Azure location: The Azure location where your resources will be deployed."),
		formatHelpNote("Azure subscription: The Azure subscription where your resources will be deployed."),
	})
}

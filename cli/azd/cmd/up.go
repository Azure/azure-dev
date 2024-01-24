package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type upFlags struct {
	provisionFlags
	deployFlags
	global *internal.GlobalCommandOptions
	envFlag
}

func (u *upFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	u.envFlag.Bind(local, global)
	u.global = global

	u.provisionFlags.bindNonCommon(local, global)
	u.provisionFlags.setCommon(&u.envFlag)
	u.deployFlags.bindNonCommon(local, global)
	u.deployFlags.setCommon(&u.envFlag)
}

func newUpFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *upFlags {
	flags := &upFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Provision Azure resources, and deploy your project with a single command.",
	}
}

type upAction struct {
	flags               *upFlags
	console             input.Console
	env                 *environment.Environment
	projectConfig       *project.ProjectConfig
	provisioningManager *provisioning.Manager
	importManager       *project.ImportManager
	workflowRunner      *WorkflowRunner
}

var defaultUpWorkflow = &workflow.Workflow{
	Name: "up",
	Steps: []*workflow.Step{
		{AzdCommand: workflow.Command{Args: []string{"package", "--all"}}},
		{AzdCommand: workflow.Command{Args: []string{"provision"}}},
		{AzdCommand: workflow.Command{Args: []string{"deploy", "--all"}}},
	},
}

func newUpAction(
	flags *upFlags,
	console input.Console,
	env *environment.Environment,
	_ auth.LoggedInGuard,
	projectConfig *project.ProjectConfig,
	provisioningManager *provisioning.Manager,
	importManager *project.ImportManager,
	workflowRunner *WorkflowRunner,
) actions.Action {
	return &upAction{
		flags:               flags,
		console:             console,
		env:                 env,
		projectConfig:       projectConfig,
		provisioningManager: provisioningManager,
		importManager:       importManager,
		workflowRunner:      workflowRunner,
	}
}

func (u *upAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	infra, err := u.importManager.ProjectInfrastructure(ctx, u.projectConfig)
	if err != nil {
		return nil, err
	}
	defer func() { _ = infra.Cleanup() }()

	err = u.provisioningManager.Initialize(ctx, u.projectConfig.Path, infra.Options)
	if err != nil {
		return nil, err
	}

	startTime := time.Now()

	upWorkflow, has := u.projectConfig.Workflows["up"]
	if !has {
		upWorkflow = defaultUpWorkflow
	} else {
		u.console.Message(ctx, output.WithGrayFormat("Note: Running custom 'up' workflow from azure.yaml"))
	}

	if err := u.workflowRunner.Run(ctx, upWorkflow); err != nil {
		return nil, err
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf("Your up workflow to provision and deploy to Azure completed in %s.",
				ux.DurationAsText(since(startTime))),
		},
	}, nil
}

func getCmdUpHelpDescription(c *cobra.Command) string {
	return generateCmdHelpDescription(
		heredoc.Docf(
			`Runs a workflow to %s, %s and %s your application in a single step.

			The %s workflow can be customized by adding a %s section to your %s.

			For example, modify the workflow to provision before packaging and deploying:

			-------------------------
			%s
			workflows:
			  up:
			    - azd: provision
			    - azd: package --all
			    - azd: deploy --all
			-------------------------

			Any azd command and flags are supported in the workflow steps.`,
			output.WithHighLightFormat("package"),
			output.WithHighLightFormat("provision"),
			output.WithHighLightFormat("deploy"),
			output.WithHighLightFormat("up"),
			output.WithHighLightFormat("workflows"),
			output.WithHighLightFormat("azure.yaml"),
			output.WithGrayFormat("# azure.yaml"),
		),
		nil,
	)
}

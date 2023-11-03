package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type upFlags struct {
	global *internal.GlobalCommandOptions
	envFlag
}

func (u *upFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	u.envFlag.Bind(local, global)
	u.global = global
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
	flags                        *upFlags
	console                      input.Console
	env                          *environment.Environment
	projectConfig                *project.ProjectConfig
	runner                       middleware.MiddlewareContext
	workflowRunActionInitializer actions.ActionInitializer[*workflowRunAction]
	provisioningManager          *provisioning.Manager
}

var defaultUpWorkflow = []*workflow.Step{
	{Command: "package --all"},
	{Command: "provision"},
	{Command: "deploy --all"},
}

func newUpAction(
	flags *upFlags,
	console input.Console,
	env *environment.Environment,
	_ auth.LoggedInGuard,
	projectConfig *project.ProjectConfig,
	workflowRunActionInitializer actions.ActionInitializer[*workflowRunAction],
	runner middleware.MiddlewareContext,
	provisioningManager *provisioning.Manager,
) actions.Action {
	return &upAction{
		flags:                        flags,
		console:                      console,
		env:                          env,
		projectConfig:                projectConfig,
		workflowRunActionInitializer: workflowRunActionInitializer,
		runner:                       runner,
		provisioningManager:          provisioningManager,
	}
}

func (u *upAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if u.projectConfig.Workflows == nil {
		u.projectConfig.Workflows = map[string][]*workflow.Step{}
	}

	upWorkflow, has := u.projectConfig.Workflows["up"]
	if !has || len(upWorkflow) == 0 {
		err := u.provisioningManager.Initialize(ctx, u.projectConfig.Path, u.projectConfig.Infra)
		if err != nil {
			return nil, err
		}

		u.projectConfig.Workflows["up"] = defaultUpWorkflow
	} else {
		u.console.Message(ctx, output.WithWarningFormat("WARNING: Running custom 'up' workflow from azure.yaml"))
	}

	workflowRunAction, err := u.workflowRunActionInitializer()
	if err != nil {
		return nil, err
	}

	workflowRunAction.args = []string{"up"}
	provisionOptions := &middleware.Options{CommandPath: "workflow run"}
	return u.runner.RunChildAction(ctx, provisionOptions, workflowRunAction)
}

func getCmdUpHelpDescription(c *cobra.Command) string {
	return generateCmdHelpDescription(
		fmt.Sprintf("Executes the %s, %s and %s commands in a single step.",
			output.WithHighLightFormat("azd package"),
			output.WithHighLightFormat("azd provision"),
			output.WithHighLightFormat("azd deploy")), nil)
}

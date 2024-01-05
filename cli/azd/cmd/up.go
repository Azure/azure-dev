package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
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
	serviceLocator      ioc.ServiceLocator
	provisioningManager *provisioning.Manager
	importManager       *project.ImportManager
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
	serviceLocator ioc.ServiceLocator,
	provisioningManager *provisioning.Manager,
	importManager *project.ImportManager,
) actions.Action {
	return &upAction{
		flags:               flags,
		console:             console,
		env:                 env,
		projectConfig:       projectConfig,
		serviceLocator:      serviceLocator,
		provisioningManager: provisioningManager,
		importManager:       importManager,
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

	upWorkflow, has := u.projectConfig.Workflows.Get("up")
	if !has {
		upWorkflow = defaultUpWorkflow
	} else {
		u.console.Message(ctx, output.WithGrayFormat("Note: Running custom 'up' workflow from azure.yaml"))
	}

	if err := u.runWorkflow(ctx, upWorkflow); err != nil {
		return nil, err
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf("Your application was provisioned and deployed to Azure in %s.",
				ux.DurationAsText(since(startTime))),
		},
	}, nil
}

func getCmdUpHelpDescription(c *cobra.Command) string {
	return generateCmdHelpDescription(
		fmt.Sprintf("Executes the %s, %s and %s commands in a single step.",
			output.WithHighLightFormat("azd package"),
			output.WithHighLightFormat("azd provision"),
			output.WithHighLightFormat("azd deploy")), nil)
}

// Execute the 'up' workflow
func (u *upAction) runWorkflow(ctx context.Context, workflow *workflow.Workflow) error {
	var rootCmd *cobra.Command
	if err := u.serviceLocator.ResolveNamed("root-cmd", &rootCmd); err != nil {
		return err
	}

	for _, step := range workflow.Steps {
		childCtx := middleware.WithChildAction(ctx)

		args := []string{}
		if step.AzdCommand.Name != "" {
			args = strings.Split(step.AzdCommand.Name, " ")
		}
		if len(step.AzdCommand.Args) > 0 {
			args = append(args, step.AzdCommand.Args...)
		}

		// Write blank line in between steps
		u.console.Message(ctx, "")

		rootCmd.SetArgs(args)
		if err := rootCmd.ExecuteContext(childCtx); err != nil {
			return fmt.Errorf("error executing step command '%s': %w", strings.Join(args, " "), err)
		}
	}

	return nil
}

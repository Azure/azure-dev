package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
)

func workflowActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("workflow", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "workflow",
			Short: "Manage workflows.",
		},
		HelpOptions: actions.ActionHelpOptions{
			Description: getCmdWorkflowHelpDescription,
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupManage,
		},
	})

	group.Add("run", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:     "run <workflow-name>",
			Short:   "Runs a workflow with the specified name.",
			Args:    cobra.ExactArgs(1),
			Example: `$ azd workflow run up`,
		},
		FlagsResolver:  newWorkflowRunFlags,
		ActionResolver: newWorkflowRunAction,
	})

	return group
}

func getCmdWorkflowHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(
		"Manage your application workflows. With this command group, you can run custom workflows.",
		[]string{},
	)
}

type workflowRunFlags struct {
}

func newWorkflowRunFlags(cmd *cobra.Command) *workflowRunFlags {
	return &workflowRunFlags{}
}

func newWorkflowRunAction(
	args []string,
	projectConfig *project.ProjectConfig,
	serviceLocator ioc.ServiceLocator,
	console input.Console,
) actions.Action {
	return &workflowRunAction{
		args:           args,
		projectConfig:  projectConfig,
		serviceLocator: serviceLocator,
		console:        console,
	}
}

type workflowRunAction struct {
	args           []string
	projectConfig  *project.ProjectConfig
	serviceLocator ioc.ServiceLocator
	console        input.Console
}

func (a *workflowRunAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	workflowName := a.args[0]

	// Command title
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: fmt.Sprintf("Running workflow '%s' (azd workflow run)", workflowName),
	})

	startTime := time.Now()

	workflow, has := a.projectConfig.Workflows[workflowName]
	if !has {
		return nil, fmt.Errorf("workflow '%s' not found", workflowName)
	}

	var rootCmd *cobra.Command
	if err := a.serviceLocator.ResolveNamed("root-cmd", &rootCmd); err != nil {
		return nil, err
	}

	for _, step := range workflow {
		args := strings.Split(step.Command, " ")
		args = append(args, step.Args...)

		rootCmd.SetArgs(args)
		if err := rootCmd.ExecuteContext(ctx); err != nil {
			return nil, fmt.Errorf("error executing step '%s': %w", step.Command, err)
		}
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf("Your workflow completed in %s.", ux.DurationAsText(since(startTime))),
		},
	}, nil
}

package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
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

func newWorkflowRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <workflow-name>",
		Short: "Runs a workflow with the specified name.",
	}
}

type workflowRunFlags struct {
}

func newWorkflowRunFlags(cmd *cobra.Command) *workflowRunFlags {
	return &workflowRunFlags{}
}

func newWorkflowRunAction(args []string, projectConfig *project.ProjectConfig, rootDescriptor *actions.ActionDescriptor, container *ioc.NestedContainer) actions.Action {
	return &workflowRunAction{
		args:           args,
		projectConfig:  projectConfig,
		rootDescriptor: rootDescriptor,
		container:      container,
	}
}

type workflowRunAction struct {
	args           []string
	projectConfig  *project.ProjectConfig
	rootDescriptor *actions.ActionDescriptor
	container      *ioc.NestedContainer
}

func (a *workflowRunAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	workflowName := a.args[0]
	fmt.Printf("running worklfow '%s'\n", workflowName)

	workflow, has := a.projectConfig.Workflows[workflowName]
	if !has {
		return nil, fmt.Errorf("workflow '%s' not found", workflowName)
	}

	var cmd *cobra.Command
	if err := a.container.ResolveNamed("root", &cmd); err != nil {
		return nil, err
	}

	for _, step := range workflow {
		args := strings.Split(step.Command, " ")
		args = append(args, step.Args...)

		cmd.SetArgs(args)
		if err := cmd.ExecuteContext(ctx); err != nil {
			return nil, fmt.Errorf("error executing step '%s': %w", step.Command, err)
		}
	}

	return nil, nil
}

func (a *workflowRunAction) findStep(command string) (*actions.ActionDescriptor, error) {
	for _, child := range a.rootDescriptor.Children() {
		if child.Name == command {
			return child, nil
		}
	}

	return nil, fmt.Errorf("step '%s' not found", command)
}

package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
	"github.com/spf13/cobra"
)

// Runner is responsible for executing a workflow
type WorkflowRunner struct {
	serviceLocator ioc.ServiceLocator
	console        input.Console
}

// NewRunner creates a new instance of the Runner
func NewWorkflowRunner(serviceLocator ioc.ServiceLocator, console input.Console) *WorkflowRunner {
	return &WorkflowRunner{
		serviceLocator: serviceLocator,
		console:        console,
	}
}

func (r *WorkflowRunner) Run(ctx context.Context, workflow *workflow.Workflow) error {
	var rootCmd *cobra.Command
	if err := r.serviceLocator.ResolveNamed("root-cmd", &rootCmd); err != nil {
		return err
	}

	for _, step := range workflow.Steps {
		childCtx := middleware.WithChildAction(ctx)

		args := []string{}
		if step.AzdCommand.Name != "" {
			args = append(args, step.AzdCommand.Name)
		}

		if len(step.AzdCommand.Args) > 0 {
			args = append(args, step.AzdCommand.Args...)
		}

		// Write blank line in between steps
		r.console.Message(ctx, "")

		rootCmd.SetArgs(args)
		if err := rootCmd.ExecuteContext(childCtx); err != nil {
			return fmt.Errorf("error executing step command '%s': %w", strings.Join(args, " "), err)
		}
	}

	return nil
}

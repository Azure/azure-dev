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
// Ideally this struct would be in the workflow package, but since it requires middleware package and cobra it will need
// to live in the cmd package until we resolve the circular dependency.
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

// Run executes the specified workflow against the root cobra command
func (r *WorkflowRunner) Run(ctx context.Context, workflow *workflow.Workflow) error {
	var rootCmd *cobra.Command
	if err := r.serviceLocator.ResolveNamed("root-cmd", &rootCmd); err != nil {
		return err
	}

	for _, step := range workflow.Steps {
		childCtx := middleware.WithChildAction(ctx)

		if len(step.AzdCommand.Args) > 0 {
			rootCmd.SetArgs(step.AzdCommand.Args)
		}

		if err := rootCmd.ExecuteContext(childCtx); err != nil {
			return fmt.Errorf("error executing step command '%s': %w", strings.Join(step.AzdCommand.Args, " "), err)
		}
	}

	return nil
}

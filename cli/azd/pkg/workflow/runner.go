package workflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

// AzdCommandRunner abstracts the execution of an azd command given an set of arguments and context.
type AzdCommandRunner interface {
	SetArgs(args []string)
	ExecuteContext(ctx context.Context) error
}

// Runner is responsible for executing a workflow
type Runner struct {
	azdRunner AzdCommandRunner
	console   input.Console
}

// NewRunner creates a new instance of the Runner.
func NewRunner(azdRunner AzdCommandRunner, console input.Console) *Runner {
	return &Runner{
		azdRunner: azdRunner,
		console:   console,
	}
}

// Run executes the specified workflow against the root cobra command
func (r *Runner) Run(ctx context.Context, workflow *Workflow) error {
	for _, step := range workflow.Steps {
		if len(step.AzdCommand.Args) > 0 {
			r.azdRunner.SetArgs(step.AzdCommand.Args)
		}

		if err := r.azdRunner.ExecuteContext(ctx); err != nil {
			return fmt.Errorf("error executing step command '%s': %w", strings.Join(step.AzdCommand.Args, " "), err)
		}
	}

	return nil
}

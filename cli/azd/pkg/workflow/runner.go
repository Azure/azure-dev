// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

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
		// Create a child context for this step to enable automatic handler cleanup
		stepCtx, cancel := context.WithCancel(ctx)

		if len(step.AzdCommand.Args) > 0 {
			r.azdRunner.SetArgs(step.AzdCommand.Args)
		}

		// Execute the step with the step-scoped context
		err := r.azdRunner.ExecuteContext(stepCtx)

		// Cancel the step context to trigger automatic cleanup of any handlers
		// registered during this step execution
		cancel()

		if err != nil {
			return fmt.Errorf("error executing step command '%s': %w", strings.Join(step.AzdCommand.Args, " "), err)
		}
	}

	return nil
}

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
	   azdRunner  AzdCommandRunner
	   console    input.Console
	   projectRoot string
}

// NewRunner creates a new instance of the Runner.
// projectRoot should be the directory containing azure.yaml
func NewRunner(azdRunner AzdCommandRunner, console input.Console, projectRoot string) *Runner {
	   return &Runner{
			   azdRunner:  azdRunner,
			   console:    console,
			   projectRoot: projectRoot,
	   }
}

// Run executes the specified workflow against the root cobra command
func (r *Runner) Run(ctx context.Context, workflow *Workflow) error {
	   for _, step := range workflow.Steps {
			   args := step.AzdCommand.Args
			   hasCwd := false
			   for _, arg := range args {
					   if arg == "--cwd" || strings.HasPrefix(arg, "--cwd=") || arg == "-C" {
							   hasCwd = true
							   break
					   }
			   }
			   if !hasCwd && r.projectRoot != "" {
					   // Prepend --cwd <projectRoot>
					   args = append([]string{"--cwd", r.projectRoot}, args...)
			   }
			   r.azdRunner.SetArgs(args)

			   if err := r.azdRunner.ExecuteContext(ctx); err != nil {
					   return fmt.Errorf("error executing step command '%s': %w", strings.Join(args, " "), err)
			   }
	   }

	   return nil
}

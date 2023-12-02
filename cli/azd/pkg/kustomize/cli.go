package kustomize

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
)

// Cli is a wrapper around the kustomize cli
type Cli struct {
	commandRunner exec.CommandRunner
	cwd           string
}

// NewCli creates a new instance of the kustomize cli
func NewCli(commandRunner exec.CommandRunner) *Cli {
	return &Cli{
		commandRunner: commandRunner,
	}
}

// WithCwd sets the working directory for the kustomize command
func (k *Cli) WithCwd(cwd string) *Cli {
	k.cwd = cwd
	return k
}

// Edit runs the kustomize edit command with the specified args
func (k *Cli) Edit(ctx context.Context, args ...string) error {
	runArgs := exec.NewRunArgs("kustomize", "edit").
		AppendParams(args...).
		WithCwd(k.cwd)

	_, err := k.commandRunner.Run(ctx, runArgs)

	if err != nil {
		return fmt.Errorf("failed running kustomize edit: %w", err)
	}

	return nil
}

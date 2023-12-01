package kustomize

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
)

type KustomizeCli interface {
	Edit(ctx context.Context, args ...string) error
	WithCwd(cwd string) KustomizeCli
}

type kustomizeCli struct {
	commandRunner exec.CommandRunner
	cwd           string
}

func (k *kustomizeCli) WithCwd(cwd string) KustomizeCli {
	k.cwd = cwd
	return k
}

func NewKustomize(commandRunner exec.CommandRunner) KustomizeCli {
	return &kustomizeCli{
		commandRunner: commandRunner,
	}
}

func (k *kustomizeCli) Edit(ctx context.Context, args ...string) error {
	runArgs := exec.NewRunArgs("kustomize", "edit").
		AppendParams(args...).
		WithCwd(k.cwd)

	_, err := k.commandRunner.Run(ctx, runArgs)

	if err != nil {
		return fmt.Errorf("failed to run kustomize edit: %w", err)
	}

	return nil
}

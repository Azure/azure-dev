package kustomize

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
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

func (cli *Cli) Name() string {
	return "kustomize"
}

// Returns the installation URL to install the Kustomize CLI
func (cli *Cli) InstallUrl() string {
	return "https://aka.ms/azure-dev/kustomize-install"
}

// Checks whether or not the Kustomize CLI is installed and available within the PATH
func (cli *Cli) CheckInstalled(ctx context.Context) error {
	if err := tools.ToolInPath("kustomize"); err != nil {
		return err
	}

	// We don't have a minimum required version of kustomize today, but
	// for diagnostics purposes, let's fetch and log the version of kustomize
	// we're using.
	if ver, err := cli.getClientVersion(ctx); err != nil {
		log.Printf("error fetching kustomize version: %s", err)
	} else {
		log.Printf("kustomize version: %s", ver)
	}

	return nil
}

// WithCwd sets the working directory for the kustomize command
func (cli *Cli) WithCwd(cwd string) *Cli {
	cli.cwd = cwd
	return cli
}

// Edit runs the kustomize edit command with the specified args
func (cli *Cli) Edit(ctx context.Context, args ...string) error {
	runArgs := exec.NewRunArgs("kustomize", "edit").
		AppendParams(args...)

	if cli.cwd != "" {
		runArgs = runArgs.WithCwd(cli.cwd)
	}

	_, err := cli.commandRunner.Run(ctx, runArgs)

	if err != nil {
		return fmt.Errorf("failed running kustomize edit: %w", err)
	}

	return nil
}

func (cli *Cli) getClientVersion(ctx context.Context) (string, error) {
	runArgs := exec.NewRunArgs("kustomize", "version")
	versionResult, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return "", fmt.Errorf("fetching kustomize version: %w", err)
	}

	return versionResult.Stdout[1:], nil
}

package helm

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
)

type Cli struct {
	commandRunner exec.CommandRunner
}

func NewCli(commandRunner exec.CommandRunner) *Cli {
	return &Cli{
		commandRunner: commandRunner,
	}
}

func (c *Cli) AddRepo(ctx context.Context, repo *Repository) error {
	runArgs := exec.NewRunArgs("helm", "repo", "add", repo.Name, repo.Url)
	_, err := c.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed to add repo %s: %w", repo.Name, err)
	}

	return nil
}

func (c *Cli) UpdateRepo(ctx context.Context, repoName string) error {
	runArgs := exec.NewRunArgs("helm", "repo", "update", repoName)
	_, err := c.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed to add repo %s: %w", repoName, err)
	}

	return nil
}

func (c *Cli) Install(ctx context.Context, release *Release) error {
	runArgs := exec.NewRunArgs("helm", "install", release.Name, release.Chart)
	if release.Values != "" {
		runArgs.AppendParams("--values", release.Values)
	}

	_, err := c.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed to install helm chart %s: %w", release.Chart, err)
	}

	return nil
}

func (c *Cli) Upgrade(ctx context.Context, release *Release) error {
	runArgs := exec.NewRunArgs("helm", "upgrade", release.Name, release.Chart, "--install")
	if release.Values != "" {
		runArgs.AppendParams("--values", release.Values)
	}

	if release.Namespace != "" {
		runArgs.AppendParams(
			"--namespace", release.Namespace,
			"--create-namespace",
		)
	}

	_, err := c.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed to install helm chart %s: %w", release.Chart, err)
	}

	return nil
}

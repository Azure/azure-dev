package helm

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
		runArgs = runArgs.AppendParams("--values", release.Values)
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
		runArgs = runArgs.AppendParams("--values", release.Values)
	}

	if release.Namespace != "" {
		runArgs = runArgs.AppendParams(
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

func (c *Cli) Status(ctx context.Context, release *Release) (*StatusResult, error) {
	runArgs := exec.NewRunArgs("helm", "status", release.Name, "--output", "json")
	if release.Namespace != "" {
		runArgs = runArgs.AppendParams("--namespace", release.Namespace)
	}

	runResult, err := c.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return nil, fmt.Errorf("failed to query status for helm chart %s: %w", release.Chart, err)
	}

	var result *StatusResult
	if err := json.Unmarshal([]byte(runResult.Stdout), &result); err != nil {
		return nil, fmt.Errorf("failed to parse status for helm chart %s: %w", release.Chart, err)
	}

	return result, nil
}

type StatusResult struct {
	Name      string     `json:"name"`
	Info      StatusInfo `json:"info"`
	Version   float64    `json:"version"`
	Namespace string     `json:"namespace"`
}

type StatusInfo struct {
	FirstDeployed time.Time  `json:"first_deployed"`
	LastDeployed  time.Time  `json:"last_deployed"`
	Status        StatusKind `json:"status"`
	Notes         string     `json:"notes"`
}

type StatusKind string

const (
	StatusKindDeployed StatusKind = "deployed"
)

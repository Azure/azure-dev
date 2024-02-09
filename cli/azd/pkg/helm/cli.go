package helm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type Cli struct {
	commandRunner exec.CommandRunner
}

func NewCli(commandRunner exec.CommandRunner) *Cli {
	return &Cli{
		commandRunner: commandRunner,
	}
}

// Gets the name of the Tool
func (cli *Cli) Name() string {
	return "helm"
}

// Returns the installation URL to install the Helm CLI
func (cli *Cli) InstallUrl() string {
	return "https://aka.ms/azure-dev/helm-install"
}

// Checks whether or not the Helm CLI is installed and available within the PATH
func (cli *Cli) CheckInstalled(ctx context.Context) error {
	if err := tools.ToolInPath("helm"); err != nil {
		return err
	}

	// We don't have a minimum required version of helm today, but
	// for diagnostics purposes, let's fetch and log the version of helm
	// we're using.
	if ver, err := cli.getClientVersion(ctx); err != nil {
		log.Printf("error fetching helm version: %s", err)
	} else {
		log.Printf("helm version: %s", ver)
	}

	return nil
}

// AddRepo adds a helm repo with the specified name and url
func (c *Cli) AddRepo(ctx context.Context, repo *Repository) error {
	runArgs := exec.NewRunArgs("helm", "repo", "add", repo.Name, repo.Url)
	_, err := c.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed to add repo %s: %w", repo.Name, err)
	}

	return nil
}

// UpdateRepo updates the helm repo with the specified name
func (c *Cli) UpdateRepo(ctx context.Context, repoName string) error {
	runArgs := exec.NewRunArgs("helm", "repo", "update", repoName)
	_, err := c.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed to add repo %s: %w", repoName, err)
	}

	return nil
}

// Install installs a helm release
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

// Upgrade upgrades a helm release to the specified version
// If the release did not previously exist, it will be installed
func (c *Cli) Upgrade(ctx context.Context, release *Release) error {
	runArgs := exec.NewRunArgs("helm", "upgrade", release.Name, release.Chart, "--install", "--wait")
	if release.Version != "" {
		runArgs = runArgs.AppendParams("--version", release.Version)
	}

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

// Status returns the status of a helm release
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

func (cli *Cli) getClientVersion(ctx context.Context) (string, error) {
	runArgs := exec.NewRunArgs("helm", "version", "--template", "{{.Version}}")
	versionResult, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return "", fmt.Errorf("fetching helm version: %w", err)
	}

	return versionResult.Stdout[1:], nil
}

// StatusResult is the result of a helm status command
type StatusResult struct {
	Name      string     `json:"name"`
	Info      StatusInfo `json:"info"`
	Version   float64    `json:"version"`
	Namespace string     `json:"namespace"`
}

// StatusInfo is the status information of a helm release
type StatusInfo struct {
	FirstDeployed time.Time  `json:"first_deployed"`
	LastDeployed  time.Time  `json:"last_deployed"`
	Status        StatusKind `json:"status"`
	Notes         string     `json:"notes"`
}

type StatusKind string

const (
	// StatusKindDeployed is the status of a helm release that has been deployed
	StatusKindDeployed StatusKind = "deployed"
)

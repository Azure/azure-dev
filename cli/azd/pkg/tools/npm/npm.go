// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package npm

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

var _ tools.ExternalTool = (*Cli)(nil)

type Cli struct {
	commandRunner exec.CommandRunner
}

func NewCli(commandRunner exec.CommandRunner) *Cli {
	return &Cli{
		commandRunner: commandRunner,
	}
}

func (cli *Cli) versionInfoNode() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 18,
			Minor: 0,
			Patch: 0},
		UpdateCommand: "Visit https://nodejs.org/en/ to upgrade",
	}
}

func (cli *Cli) CheckInstalled(ctx context.Context) error {
	err := cli.commandRunner.ToolInPath("npm")
	if err != nil {
		return err
	}

	//check node version
	nodeRes, err := tools.ExecuteCommand(ctx, cli.commandRunner, "node", "--version")
	if err != nil {
		return fmt.Errorf("checking %s version: %w", cli.Name(), err)
	}
	nodeSemver, err := tools.ExtractVersion(nodeRes)
	if err != nil {
		return fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetailNode := cli.versionInfoNode()
	if nodeSemver.Compare(updateDetailNode.MinimumVersion) == -1 {
		return &tools.ErrSemver{ToolName: "Node.js", VersionInfo: updateDetailNode}
	}

	return nil
}

func (cli *Cli) InstallUrl() string {
	return "https://nodejs.org/"
}

func (cli *Cli) Name() string {
	return "npm CLI"
}

// Install runs npm install in the specified project directory.
// Optional env parameter allows passing additional environment variables to the npm process.
func (cli *Cli) Install(ctx context.Context, project string, env []string) error {
	runArgs := exec.
		NewRunArgs("npm", "install").
		WithCwd(project)

	if len(env) > 0 {
		runArgs = runArgs.WithEnv(env)
	}

	_, err := cli.commandRunner.Run(ctx, runArgs)

	if err != nil {
		return fmt.Errorf("failed to install project %s: %w", project, err)
	}
	return nil
}

// RunScript runs an npm script in the specified project directory.
// Optional env parameter allows passing additional environment variables to the npm process.
func (cli *Cli) RunScript(ctx context.Context, projectPath string, scriptName string, env []string) error {
	runArgs := exec.
		NewRunArgs("npm", "run", scriptName, "--if-present").
		WithCwd(projectPath)

	if len(env) > 0 {
		runArgs = runArgs.WithEnv(env)
	}

	_, err := cli.commandRunner.Run(ctx, runArgs)

	if err != nil {
		return fmt.Errorf("failed to run NPM script %s, %w", scriptName, err)
	}

	return nil
}

// Prune removes extraneous packages from the node_modules directory.
// Optional env parameter allows passing additional environment variables to the npm process.
func (cli *Cli) Prune(ctx context.Context, projectPath string, production bool, env []string) error {
	runArgs := exec.
		NewRunArgs("npm", "prune").
		WithCwd(projectPath)

	if production {
		runArgs = runArgs.AppendParams("--production")
	}

	if len(env) > 0 {
		runArgs = runArgs.WithEnv(env)
	}

	_, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed pruning NPM packages, %w", err)
	}

	return nil
}

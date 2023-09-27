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

type NpmCli interface {
	tools.ExternalTool
	Install(ctx context.Context, project string) error

	// RunScript runs the given npm script (if it exists) in the project.
	//
	// Returns an error only if the script execution fails. If the script doesn't exist, no error is returned.
	RunScript(ctx context.Context, projectPath string, scriptName string) error
	Prune(ctx context.Context, projectPath string, production bool) error
}

type npmCli struct {
	commandRunner exec.CommandRunner
}

func NewNpmCli(commandRunner exec.CommandRunner) NpmCli {
	return &npmCli{
		commandRunner: commandRunner,
	}
}

func (cli *npmCli) versionInfoNode() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 18,
			Minor: 0,
			Patch: 0},
		UpdateCommand: "Visit https://nodejs.org/en/ to upgrade",
	}
}

func (cli *npmCli) CheckInstalled(ctx context.Context) error {
	err := tools.ToolInPath("npm")
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

func (cli *npmCli) InstallUrl() string {
	return "https://nodejs.org/"
}

func (cli *npmCli) Name() string {
	return "npm CLI"
}

func (cli *npmCli) Install(ctx context.Context, project string) error {
	runArgs := exec.
		NewRunArgs("npm", "install").
		WithCwd(project)

	_, err := cli.commandRunner.Run(ctx, runArgs)

	if err != nil {
		return fmt.Errorf("failed to install project %s: %w", project, err)
	}
	return nil
}

func (cli *npmCli) RunScript(ctx context.Context, projectPath string, scriptName string) error {
	runArgs := exec.
		NewRunArgs("npm", "run", scriptName, "--if-present").
		WithCwd(projectPath)

	_, err := cli.commandRunner.Run(ctx, runArgs)

	if err != nil {
		return fmt.Errorf("failed to run NPM script %s, %w", scriptName, err)
	}

	return nil
}

func (cli *npmCli) Prune(ctx context.Context, projectPath string, production bool) error {
	runArgs := exec.
		NewRunArgs("npm", "prune").
		WithCwd(projectPath)

	if production {
		runArgs = runArgs.AppendParams("--production")
	}

	_, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed pruning NPM packages, %w", err)
	}

	return nil
}

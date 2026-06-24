// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package golang

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

var _ tools.ExternalTool = (*Cli)(nil)

// Cli wraps the Go CLI for building Go projects.
type Cli struct {
	commandRunner exec.CommandRunner
}

// NewCli creates a new instance of the Go CLI wrapper.
func NewCli(commandRunner exec.CommandRunner) *Cli {
	return &Cli{
		commandRunner: commandRunner,
	}
}

func (cli *Cli) versionInfo() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 1,
			Minor: 24,
			Patch: 0,
		},
		UpdateCommand: "Visit https://go.dev/dl/ to upgrade",
	}
}

// CheckInstalled verifies Go is installed and meets the minimum version.
func (cli *Cli) CheckInstalled(ctx context.Context) error {
	if err := cli.commandRunner.ToolInPath("go"); err != nil {
		return err
	}

	goVer, err := tools.ExecuteCommand(ctx, cli.commandRunner, "go", "version")
	if err != nil {
		return fmt.Errorf("checking %s version: %w", cli.Name(), err)
	}

	log.Printf("go version: %s", goVer)

	ver, err := tools.ExtractVersion(goVer)
	if err != nil {
		return fmt.Errorf("converting to semver version fails: %w", err)
	}

	info := cli.versionInfo()
	if ver.LT(info.MinimumVersion) {
		return &tools.ErrSemver{ToolName: cli.Name(), VersionInfo: info}
	}

	return nil
}

// InstallUrl returns the URL for installing Go.
func (cli *Cli) InstallUrl() string {
	return "https://go.dev/dl/"
}

// Name returns the display name of the tool.
func (cli *Cli) Name() string {
	return "Go CLI"
}

// Build compiles the Go project at the given directory, outputting
// the binary with the specified name. Environment variables are passed
// to the build command to support cross-compilation.
func (cli *Cli) Build(
	ctx context.Context,
	projectDir string,
	outputName string,
	env []string,
) error {
	_, err := cli.commandRunner.Run(ctx, exec.RunArgs{
		Cmd:  "go",
		Args: []string{"build", "-o", outputName, "."},
		Cwd:  projectDir,
		Env:  env,
	})
	if err != nil {
		return fmt.Errorf("building Go project: %w", err)
	}

	return nil
}

// ModDownload runs 'go mod download' to fetch dependencies.
func (cli *Cli) ModDownload(ctx context.Context, projectDir string, env []string) error {
	_, err := cli.commandRunner.Run(ctx, exec.RunArgs{
		Cmd:  "go",
		Args: []string{"mod", "download"},
		Cwd:  projectDir,
		Env:  env,
	})
	if err != nil {
		return fmt.Errorf("downloading Go modules: %w", err)
	}

	return nil
}

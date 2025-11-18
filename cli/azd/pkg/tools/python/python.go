// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package python

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

type Cli struct {
	commandRunner exec.CommandRunner
}

func NewCli(commandRunner exec.CommandRunner) *Cli {
	return &Cli{
		commandRunner: commandRunner,
	}
}

func (cli *Cli) versionInfo() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 3,
			Minor: 7,
			Patch: 6},
		UpdateCommand: "Visit https://www.python.org/downloads/ to upgrade",
	}
}

func (cli *Cli) CheckInstalled(ctx context.Context) error {
	pyString, err := cli.checkPath()
	if err != nil {
		return err
	}
	pythonRes, err := tools.ExecuteCommand(ctx, cli.commandRunner, pyString, "--version")
	if err != nil {
		return fmt.Errorf("checking %s version: %w", cli.Name(), err)
	}

	log.Printf("python version: %s", pythonRes)

	pythonSemver, err := tools.ExtractVersion(pythonRes)
	if err != nil {
		return fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetail := cli.versionInfo()
	if pythonSemver.LT(updateDetail.MinimumVersion) {
		return &tools.ErrSemver{ToolName: cli.Name(), VersionInfo: updateDetail}
	}
	return nil
}

func (cli *Cli) InstallUrl() string {
	return "https://wiki.python.org/moin/BeginnersGuide/Download"
}

func (cli *Cli) Name() string {
	return "Python CLI"
}

func (cli *Cli) InstallRequirements(ctx context.Context, workingDir, environment, requirementFile string) error {
	args := []string{"-m", "pip", "install", "-r", requirementFile}
	_, err := cli.Run(ctx, workingDir, environment, args...)
	if err != nil {
		return fmt.Errorf("failed to install requirements for project '%s': %w", workingDir, err)
	}

	return nil
}

func (cli *Cli) CreateVirtualEnv(ctx context.Context, workingDir, name string) error {
	pyString, err := cli.checkPath()
	if err != nil {
		return err
	}

	runArgs := exec.
		NewRunArgs(pyString, "-m", "venv", name).
		WithCwd(workingDir)

	_, err = cli.commandRunner.Run(ctx, runArgs)

	if err != nil {
		return fmt.Errorf(
			"failed to create virtual Python environment for project '%s': %w",
			workingDir,
			err)
	}
	return nil
}

func (cli *Cli) Run(
	ctx context.Context,
	workingDir string,
	environment string,
	args ...string,
) (*exec.RunResult, error) {
	pyString, err := cli.checkPath()
	if err != nil {
		return nil, err
	}

	var envActivationCmd string

	// Windows & Posix have different activation scripts
	if runtime.GOOS == "windows" {
		envActivationCmd = filepath.Join(environment, "Scripts", "activate")
	} else {
		envActivationCmd = ". " + filepath.Join(environment, "bin", "activate")
	}

	runCmd := strings.Join(append([]string{pyString}, args...), " ")
	// We need to ensure the virtual environment is activated before running the script
	commands := []string{envActivationCmd, runCmd}
	runArgs := exec.NewRunArgs("").WithCwd(workingDir)
	runResult, err := cli.commandRunner.RunList(ctx, commands, runArgs)

	if err != nil {
		return nil, fmt.Errorf("failed to run Python script: %w", err)
	}

	return &runResult, nil
}

func (cli *Cli) checkPath() (string, error) {
	if runtime.GOOS == "windows" {
		// py for https://peps.python.org/pep-0397
		// order is important. we want to resolve 'py', if available, first
		pyStrings := [2]string{"py", "python"}

		var lastErr error
		for _, py := range pyStrings {
			err := cli.commandRunner.ToolInPath(py)
			if err == nil {
				return py, nil
			}
			lastErr = err
		}
		return "", lastErr
	} else {
		err := cli.commandRunner.ToolInPath("python3")
		if err == nil {
			return "python3", nil
		}
		return "", err
	}
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package python

import (
	"context"
	"fmt"
	"log"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

type PythonCli struct {
	commandRunner exec.CommandRunner
}

func NewPythonCli(commandRunner exec.CommandRunner) *PythonCli {
	return &PythonCli{
		commandRunner: commandRunner,
	}
}

func (cli *PythonCli) versionInfo() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 3,
			Minor: 7,
			Patch: 6},
		UpdateCommand: "Visit https://www.python.org/downloads/ to upgrade",
	}
}

func (cli *PythonCli) CheckInstalled(ctx context.Context) error {
	pyString, err := checkPath()
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

func (cli *PythonCli) InstallUrl() string {
	return "https://wiki.python.org/moin/BeginnersGuide/Download"
}

func (cli *PythonCli) Name() string {
	return "Python CLI"
}

func (cli *PythonCli) InstallRequirements(ctx context.Context, workingDir, environment, requirementFile string) error {
	args := []string{"-m", "pip", "install", "-r", requirementFile}
	_, err := cli.Run(ctx, workingDir, environment, args...)
	if err != nil {
		return fmt.Errorf("failed to install requirements for project '%s': %w", workingDir, err)
	}

	return nil
}

func (cli *PythonCli) CreateVirtualEnv(ctx context.Context, workingDir, name string) error {
	pyString, err := checkPath()
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

func (cli *PythonCli) Run(
	ctx context.Context,
	workingDir string,
	environment string,
	args ...string,
) (*exec.RunResult, error) {
	pyString, err := checkPath()
	if err != nil {
		return nil, err
	}

	var runResult exec.RunResult
	var runErr error

	if runtime.GOOS == "windows" {
		// Unfortunately neither cmd.exe, nor PowerShell provide a straightforward way to use a script
		// to modify environment for command(s) in a command list.
		// So we are going to cheat and replicate the core functionality of Python venv scripts here,
		// which boils down to setting VIRTUAL_ENV environment variable.
		absWorkingDir, pathErr := filepath.Abs(workingDir)
		if pathErr != nil {
			return nil, pathErr
		}

		vEnvSetting := fmt.Sprintf("VIRTUAL_ENV=%s", path.Join(absWorkingDir, environment))

		runArgs := exec.
			NewRunArgs(pyString, args...).
			WithCwd(workingDir).
			WithEnv([]string{vEnvSetting})

		runResult, runErr = cli.commandRunner.Run(ctx, runArgs)
	} else {
		// We need to ensure the virtual environment is activated before running the script
		envActivation := ". " + path.Join(environment, "bin", "activate")
		allArgs := append([]string{pyString}, args...)
		runCmd := strings.Join(allArgs, " ")
		commands := []string{envActivation, runCmd}

		runArgs := exec.NewRunArgs(pyString).WithCwd(workingDir)
		runResult, runErr = cli.commandRunner.RunList(ctx, commands, runArgs)
	}

	if runErr != nil {
		return nil, fmt.Errorf("failed to run Python script: %w", runErr)
	}

	return &runResult, nil
}

func checkPath() (pyString string, err error) {
	if runtime.GOOS == "windows" {
		// py for https://peps.python.org/pep-0397
		// order is important. we want to resolve 'py', if available, first
		pyString := [2]string{"py", "python"}

		for _, py := range pyString {
			err = tools.ToolInPath(py)
			if err == nil {
				return py, nil
			}
		}
		return "", err
	} else {
		err := tools.ToolInPath("python3")
		if err == nil {
			return "python3", err
		}
		return "", err
	}
}

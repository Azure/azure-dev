// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package python

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"runtime"

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

func (cli *PythonCli) CheckInstalled(ctx context.Context) (bool, error) {
	pyString, found, err := checkPath()
	if !found {
		return false, err
	}
	pythonRes, err := tools.ExecuteCommand(ctx, cli.commandRunner, pyString, "--version")
	if err != nil {
		return false, fmt.Errorf("checking %s version: %w", cli.Name(), err)
	}
	pythonSemver, err := tools.ExtractVersion(pythonRes)
	if err != nil {
		return false, fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetail := cli.versionInfo()
	if pythonSemver.LT(updateDetail.MinimumVersion) {
		return false, &tools.ErrSemver{ToolName: cli.Name(), VersionInfo: updateDetail}
	}
	return true, nil
}

func (cli *PythonCli) InstallUrl() string {
	return "https://wiki.python.org/moin/BeginnersGuide/Download"
}

func (cli *PythonCli) Name() string {
	return "Python CLI"
}

func (cli *PythonCli) InstallRequirements(ctx context.Context, workingDir, environment, requirementFile string) error {
	var res exec.RunResult
	var err error

	pyString, _, err := checkPath()
	if err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		// Unfortunately neither cmd.exe, nor PowerShell provide a straightforward way to use a script
		// to modify environment for command(s) in a command list.
		// So we are going to cheat and replicate the core functionality of Python venv scripts here,
		// which boils down to setting VIRTUAL_ENV environment variable.
		absWorkingDir, pathErr := filepath.Abs(workingDir)
		if pathErr != nil {
			return pathErr
		}

		vEnvSetting := fmt.Sprintf("VIRTUAL_ENV=%s", path.Join(absWorkingDir, environment))

		runArgs := exec.
			NewRunArgs(pyString, "-m", "pip", "install", "-r", requirementFile).
			WithCwd(workingDir).
			WithEnv([]string{vEnvSetting})

		res, err = cli.commandRunner.Run(ctx, runArgs)
	} else {
		envActivation := ". " + path.Join(environment, "bin", "activate")
		installCmd := fmt.Sprintf("%s -m pip install -r %s", pyString, requirementFile)
		commands := []string{envActivation, installCmd}

		runArgs := exec.NewRunArgs("").WithCwd(workingDir)
		res, err = cli.commandRunner.RunList(ctx, commands, runArgs)
	}

	if err != nil {
		return fmt.Errorf("failed to install requirements for project '%s': %w (%s)", workingDir, err, res.String())
	}
	return nil
}

func (cli *PythonCli) CreateVirtualEnv(ctx context.Context, workingDir, name string) error {
	pyString, _, err := checkPath()
	if err != nil {
		return err
	}

	runArgs := exec.
		NewRunArgs(pyString, "-m", "venv", name).
		WithCwd(workingDir)

	res, err := cli.commandRunner.Run(ctx, runArgs)

	if err != nil {
		return fmt.Errorf(
			"failed to create virtual Python environment for project '%s': %w (%s)",
			workingDir,
			err,
			res.String(),
		)
	}
	return nil
}

func checkPath() (pyString string, found bool, err error) {
	if runtime.GOOS == "windows" {
		// py for https://peps.python.org/pep-0397
		// order is important. we want to resolve 'py', if available, first
		pyString := [2]string{"py", "python"}

		for _, py := range pyString {
			found, err = tools.ToolInPath(py)
			if found && err == nil {
				return py, found, nil
			}
		}
		return "", found, err
	} else {
		found, err := tools.ToolInPath("python3")
		if found && err == nil {
			return "python3", found, err
		}
		return "", found, err
	}
}

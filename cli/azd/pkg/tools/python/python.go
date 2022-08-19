// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package python

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"runtime"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

type PythonCli struct{}

func NewPythonCli() *PythonCli {
	return &PythonCli{}
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
	found, err := tools.ToolInPath(pythonExe())
	if !found {
		return false, err
	}
	pythonRes, err := tools.ExecuteCommand(ctx, pythonExe(), "--version")
	if err != nil {
		return false, fmt.Errorf("checking %s version: %w", cli.Name(), err)
	}
	pythonSemver, err := tools.ExtractSemver(pythonRes)
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
	var res executil.RunResult
	var err error

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

		res, err = executil.RunCommandWithShellAndEnvAndCwd(ctx, pythonExe(), []string{
			"-m", "pip", "install", "-r", requirementFile,
		}, []string{vEnvSetting}, workingDir)
	} else {
		envActivation := ". " + path.Join(environment, "bin", "activate")
		installCmd := fmt.Sprintf("%s -m pip install -r %s", pythonExe(), requirementFile)
		commands := []string{envActivation, installCmd}

		res, err = executil.RunCommandList(ctx, commands, nil, workingDir)
	}

	if err != nil {
		return fmt.Errorf("failed to install requirements for project '%s': %w (%s)", workingDir, err, res.String())
	}
	return nil
}

func (cli *PythonCli) CreateVirtualEnv(ctx context.Context, workingDir, name string) error {
	res, err := executil.RunCommandWithShellAndEnvAndCwd(ctx, pythonExe(), []string{
		"-m", "venv", name,
	}, nil, workingDir)
	if err != nil {
		return fmt.Errorf("failed to create virtual Python environment for project '%s': %w (%s)", workingDir, err, res.String())
	}
	return nil
}

func pythonExe() string {
	if runtime.GOOS == "windows" {
		return "py" // https://peps.python.org/pep-0397
	} else {
		return "python3"
	}
}

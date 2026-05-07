// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package python

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
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

func (cli *Cli) InstallRequirements(
	ctx context.Context, workingDir, environment, requirementFile string, env []string,
) error {
	args := []string{"-m", "pip", "install", "-r", requirementFile}
	_, err := cli.Run(ctx, workingDir, environment, env, args...)
	if err != nil {
		return fmt.Errorf("failed to install requirements for project '%s': %w", workingDir, err)
	}

	return nil
}

// InstallProject installs dependencies from pyproject.toml using pip.
func (cli *Cli) InstallProject(ctx context.Context, workingDir, environment string, env []string) error {
	args := []string{"-m", "pip", "install", "."}
	_, err := cli.Run(ctx, workingDir, environment, env, args...)
	if err != nil {
		return fmt.Errorf(
			"failed to install project from pyproject.toml for '%s': %w",
			workingDir, err)
	}

	return nil
}

func (cli *Cli) CreateVirtualEnv(ctx context.Context, workingDir, name string, env []string) error {
	pyString, err := cli.checkPath()
	if err != nil {
		return err
	}

	runArgs := exec.
		NewRunArgs(pyString, "-m", "venv", name).
		WithCwd(workingDir).
		WithEnv(env)

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
	env []string,
	args ...string,
) (*exec.RunResult, error) {
	pyString, err := cli.checkPath()
	if err != nil {
		return nil, err
	}

	envActivationCmd := VenvActivateCmd(environment)

	runCmd := strings.Join(append([]string{pyString}, args...), " ")
	// We need to ensure the virtual environment is activated before running the script
	commands := []string{envActivationCmd, runCmd}
	runArgs := exec.NewRunArgs("").WithCwd(workingDir).WithEnv(env)
	runResult, err := cli.commandRunner.RunList(ctx, commands, runArgs)

	if err != nil {
		return nil, fmt.Errorf("failed to run Python script: %w", err)
	}

	return &runResult, nil
}

// ResolveCommand returns the platform-appropriate Python
// command name. On Windows it prefers "py" (PEP 397 launcher),
// falling back to "python"; on other platforms it uses
// "python3".
func (cli *Cli) ResolveCommand() (string, error) {
	return cli.checkPath()
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

// EnsureVirtualEnv creates the virtual environment if it does
// not already exist. If the venv directory exists, creation is
// skipped. Non-directory paths and inaccessible paths are
// reported as errors.
func (cli *Cli) EnsureVirtualEnv(
	ctx context.Context,
	workingDir, name string,
	env []string,
) error {
	venvPath := filepath.Join(workingDir, name)
	info, statErr := os.Stat(venvPath)
	if statErr == nil {
		if !info.IsDir() {
			return fmt.Errorf(
				"venv path %q exists but is not "+
					"a directory",
				venvPath,
			)
		}
		return nil
	}

	if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf(
			"virtual environment at %q is not "+
				"accessible (check file "+
				"permissions): %w",
			venvPath, statErr,
		)
	}

	if err := cli.CreateVirtualEnv(ctx, workingDir, name, env); err != nil {
		return fmt.Errorf(
			"%w — ensure Python 3.3+ is installed with the venv module",
			err,
		)
	}
	return nil
}

// InstallDependencies dispatches dependency installation based
// on the given dependency file name. It calls
// [Cli.InstallRequirements] for "requirements.txt" and
// [Cli.InstallProject] for "pyproject.toml". Unrecognized file
// names are logged and skipped.
func (cli *Cli) InstallDependencies(
	ctx context.Context,
	dir, venvName, depFile string,
	env []string,
) error {
	switch depFile {
	case "requirements.txt":
		if err := cli.InstallRequirements(
			ctx, dir, venvName, depFile, env,
		); err != nil {
			return fmt.Errorf(
				"%w — check that the file is valid and all packages are available",
				err,
			)
		}
		return nil
	case "pyproject.toml":
		if err := cli.InstallProject(
			ctx, dir, venvName, env,
		); err != nil {
			return fmt.Errorf(
				"%w — check the [build-system] section and ensure pip >= 21.3",
				err,
			)
		}
		return nil
	default:
		log.Printf(
			"unsupported dependency file %q - skipping install",
			depFile,
		)
	}
	return nil
}

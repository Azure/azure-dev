// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package language

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// pythonTools abstracts the Python CLI operations needed by
// pythonExecutor, decoupling it from the concrete [python.Cli]
// for testability. [python.Cli] satisfies this interface.
type pythonTools interface {
	CheckInstalled(ctx context.Context) error
	CreateVirtualEnv(
		ctx context.Context,
		workingDir, name string,
		env []string,
	) error
	InstallRequirements(
		ctx context.Context,
		workingDir, environment, requirementFile string,
		env []string,
	) error
	InstallProject(
		ctx context.Context,
		workingDir, environment string,
		env []string,
	) error
}

// pythonExecutor implements [ScriptExecutor] for Python scripts.
// It manages virtual environment creation and dependency
// installation when a project file (requirements.txt or
// pyproject.toml) is discovered near the script.
type pythonExecutor struct {
	commandRunner exec.CommandRunner
	pythonCli     pythonTools
	boundaryDir   string   // project/service root for discovery
	cwd           string   // working directory for execution
	envVars       []string // environment variables for execution

	// venvPath is set by Prepare when a project context with a
	// dependency file is discovered. Empty means system Python.
	venvPath string
}

// newPythonExecutor creates a pythonExecutor configured for the
// given execution context. The boundaryDir limits project file
// discovery; cwd sets the working directory for script execution;
// envVars are forwarded to all child processes.
func newPythonExecutor(
	commandRunner exec.CommandRunner,
	pythonCli pythonTools,
	boundaryDir string,
	cwd string,
	envVars []string,
) *pythonExecutor {
	return &pythonExecutor{
		commandRunner: commandRunner,
		pythonCli:     pythonCli,
		boundaryDir:   boundaryDir,
		cwd:           cwd,
		envVars:       envVars,
	}
}

// Prepare verifies that Python is installed and, when a project
// file is found, creates a virtual environment and installs
// dependencies. The venv naming convention follows
// [framework_service_python.go]: {projectDirName}_env.
func (e *pythonExecutor) Prepare(
	ctx context.Context,
	scriptPath string,
) error {
	// 1. Verify Python is installed.
	if err := e.pythonCli.CheckInstalled(ctx); err != nil {
		return fmt.Errorf(
			"python 3 is required to run this hook but was not found on PATH. "+
				"Install Python from https://www.python.org/downloads/ : %w",
			err,
		)
	}

	// 2. Discover project context for dependency installation.
	projCtx, err := DiscoverProjectFile(
		scriptPath, e.boundaryDir,
	)
	if err != nil {
		return fmt.Errorf(
			"discovering project file: %w", err,
		)
	}

	// No project file — run with system Python directly.
	if projCtx == nil {
		return nil
	}

	// 3. Set up virtual environment.
	venvName := venvNameForDir(projCtx.ProjectDir)
	venvPath := filepath.Join(projCtx.ProjectDir, venvName)

	if err := e.ensureVenv(
		ctx, projCtx.ProjectDir, venvName, venvPath,
	); err != nil {
		return err
	}

	// 4. Install dependencies from the discovered file.
	depFile := filepath.Base(projCtx.DependencyFile)
	if err := e.installDeps(
		ctx, projCtx.ProjectDir, venvName, depFile,
	); err != nil {
		return err
	}

	e.venvPath = venvPath
	return nil
}

// ensureVenv creates the virtual environment if it does not
// already exist. If the venv directory exists, creation is
// skipped. Non-existence errors (e.g. permission denied) are
// propagated immediately.
func (e *pythonExecutor) ensureVenv(
	ctx context.Context,
	projectDir, venvName, venvPath string,
) error {
	_, statErr := os.Stat(venvPath)
	if statErr == nil {
		// Venv directory already exists — skip creation.
		return nil
	}

	if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf(
			"virtual environment at %q is not accessible "+
				"(check file permissions): %w",
			venvPath, statErr,
		)
	}

	if err := e.pythonCli.CreateVirtualEnv(
		ctx, projectDir, venvName, e.envVars,
	); err != nil {
		return fmt.Errorf(
			"creating python virtual environment at %q failed. "+
				"Ensure Python 3.3+ is installed with the venv module: %w",
			filepath.Join(projectDir, venvName), err,
		)
	}
	return nil
}

// installDeps installs Python dependencies from the given file
// into the virtual environment identified by venvName.
func (e *pythonExecutor) installDeps(
	ctx context.Context,
	projectDir, venvName, depFile string,
) error {
	switch depFile {
	case "requirements.txt":
		if err := e.pythonCli.InstallRequirements(
			ctx, projectDir, venvName, depFile, e.envVars,
		); err != nil {
			return fmt.Errorf(
				"installing python requirements from %s. "+
					"Check that the file is valid and all packages are available: %w",
				depFile, err,
			)
		}
	case "pyproject.toml":
		if err := e.pythonCli.InstallProject(
			ctx, projectDir, venvName, e.envVars,
		); err != nil {
			return fmt.Errorf(
				"installing python project from pyproject.toml. "+
					"Check the [build-system] section and ensure pip >= 21.3: %w",
				err,
			)
		}
	}
	return nil
}

// Execute runs the Python script at the given path. When Prepare
// has configured a virtual environment, the venv's Python binary
// is used; otherwise the system Python is resolved using the
// same platform heuristics as [python.Cli].
func (e *pythonExecutor) Execute(
	ctx context.Context,
	scriptPath string,
	options tools.ExecOptions,
) (exec.RunResult, error) {
	pyCmd := e.resolvePythonPath()

	runArgs := exec.
		NewRunArgs(pyCmd, scriptPath).
		WithEnv(e.envVars)

	// Prefer configured cwd; fall back to script's directory.
	cwd := e.cwd
	if cwd == "" {
		cwd = filepath.Dir(scriptPath)
	}
	runArgs = runArgs.WithCwd(cwd)

	if options.Interactive != nil {
		runArgs = runArgs.WithInteractive(
			*options.Interactive,
		)
	}
	if options.StdOut != nil {
		runArgs = runArgs.WithStdOut(options.StdOut)
	}

	return e.commandRunner.Run(ctx, runArgs)
}

// resolvePythonPath returns the path to the Python executable.
// When a virtual environment was configured by [Prepare], it
// returns the venv's Python binary; otherwise it falls back to
// the system-level Python command.
func (e *pythonExecutor) resolvePythonPath() string {
	if e.venvPath != "" {
		if runtime.GOOS == "windows" {
			return filepath.Join(
				e.venvPath, "Scripts", "python.exe",
			)
		}
		return filepath.Join(e.venvPath, "bin", "python")
	}
	return resolvePythonCmd(e.commandRunner)
}

// resolvePythonCmd returns the platform-appropriate Python
// command name, following the same resolution strategy as
// [python.Cli]: on Windows it prefers "py" (PEP 397 launcher),
// falling back to "python"; on other platforms it uses "python3".
func resolvePythonCmd(
	commandRunner exec.CommandRunner,
) string {
	if runtime.GOOS == "windows" {
		// Try py launcher first (PEP 397), then python.
		for _, cmd := range []string{"py", "python"} {
			if commandRunner.ToolInPath(cmd) == nil {
				return cmd
			}
		}
		// Fallback even if not found — Prepare() will catch this.
		return "python"
	}
	// Unix: python3 is the standard command.
	if commandRunner.ToolInPath("python3") == nil {
		return "python3"
	}
	// Fallback — Prepare() will catch missing Python.
	return "python3"
}

// venvNameForDir computes a virtual environment directory name
// from the given project directory path. It follows the naming
// convention in [framework_service_python.go]: {baseName}_env.
func venvNameForDir(projectDir string) string {
	trimmed := strings.TrimSpace(projectDir)
	if len(trimmed) > 0 &&
		trimmed[len(trimmed)-1] == os.PathSeparator {
		trimmed = trimmed[:len(trimmed)-1]
	}
	_, base := filepath.Split(trimmed)
	return base + "_env"
}

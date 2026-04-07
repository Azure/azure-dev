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
	"github.com/azure/azure-dev/cli/azd/pkg/tools/python"
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

// pythonExecutor implements [tools.HookExecutor] for Python scripts.
// It manages virtual environment creation and dependency
// installation when a project file (requirements.txt or
// pyproject.toml) is discovered near the script.
type pythonExecutor struct {
	commandRunner exec.CommandRunner
	pythonCli     pythonTools

	// venvPath is set by Prepare when a project context with a
	// dependency file is discovered. Empty means system Python.
	venvPath string
}

// NewPythonExecutor creates a Python HookExecutor. Takes only IoC-injectable deps.
func NewPythonExecutor(
	commandRunner exec.CommandRunner,
	pythonCli *python.Cli,
) tools.HookExecutor {
	return newPythonExecutorInternal(commandRunner, pythonCli)
}

// newPythonExecutorInternal creates a pythonExecutor using the
// pythonTools interface. This allows tests to inject mocks.
func newPythonExecutorInternal(
	commandRunner exec.CommandRunner,
	pythonCli pythonTools,
) *pythonExecutor {
	return &pythonExecutor{
		commandRunner: commandRunner,
		pythonCli:     pythonCli,
	}
}

// Prepare verifies that Python is installed and, when a project
// file is found, creates a virtual environment and installs
// dependencies. The venv naming convention follows
// [framework_service_python.go]: {projectDirName}_env.
//
// Before creating a new venv, Prepare checks for an existing
// virtual environment — either via the VIRTUAL_ENV environment
// variable or well-known directory names (.venv, venv) in the
// project path. When an existing venv is detected, creation is
// skipped but dependency installation still runs when a Python
// project file is present and the venv is inside the project.
func (e *pythonExecutor) Prepare(
	ctx context.Context,
	scriptPath string,
	execCtx tools.ExecutionContext,
) error {
	// 1. Verify Python is installed.
	if err := e.pythonCli.CheckInstalled(ctx); err != nil {
		return fmt.Errorf(
			"python 3 is required to run this hook "+
				"but was not found on PATH. Install "+
				"Python from https://www.python.org"+
				"/downloads/ : %w",
			err,
		)
	}

	// 2. Discover project context for dependency installation.
	projCtx, err := DiscoverProjectFile(
		scriptPath, execCtx.BoundaryDir,
	)
	if err != nil {
		return fmt.Errorf(
			"discovering project file: %w", err,
		)
	}

	// 3. Detect existing virtual environment (VIRTUAL_ENV
	//    env var or well-known directories).
	if existing := detectExistingVenv(
		execCtx, projCtx,
	); existing != "" {
		e.venvPath = existing

		// Still install deps when a Python project file
		// exists and the venv lives inside projectDir (so
		// the python CLI can locate the activation script).
		if projCtx != nil &&
			projCtx.Language == ScriptLanguagePython {
			if name, ok := relativeVenvName(
				projCtx.ProjectDir, existing,
			); ok {
				depFile := filepath.Base(
					projCtx.DependencyFile,
				)
				if err := e.installDeps(
					ctx, projCtx.ProjectDir,
					name, depFile,
					execCtx.EnvVars,
				); err != nil {
					return err
				}
			}
		}
		return nil
	}

	// No project file — run with system Python directly.
	if projCtx == nil {
		return nil
	}

	// Skip venv setup if the discovered project is not
	// Python (e.g. a package.json for JS living near the
	// Python script).
	if projCtx.Language != ScriptLanguagePython {
		return nil
	}

	// 4. Set up virtual environment.
	venvName := venvNameForDir(projCtx.ProjectDir)
	venvPath := filepath.Join(
		projCtx.ProjectDir, venvName,
	)

	if err := e.ensureVenv(
		ctx, projCtx.ProjectDir,
		venvName, venvPath, execCtx.EnvVars,
	); err != nil {
		return err
	}

	// 5. Install dependencies from the discovered file.
	depFile := filepath.Base(projCtx.DependencyFile)
	if err := e.installDeps(
		ctx, projCtx.ProjectDir,
		venvName, depFile, execCtx.EnvVars,
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
	envVars []string,
) error {
	info, statErr := os.Stat(venvPath)
	if statErr == nil {
		if !info.IsDir() {
			return fmt.Errorf(
				"venv path %q exists but is not a directory",
				venvPath,
			)
		}
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
		ctx, projectDir, venvName, envVars,
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
	envVars []string,
) error {
	switch depFile {
	case "requirements.txt":
		if err := e.pythonCli.InstallRequirements(
			ctx, projectDir, venvName, depFile, envVars,
		); err != nil {
			return fmt.Errorf(
				"installing python requirements from %s. "+
					"Check that the file is valid and all packages are available: %w",
				depFile, err,
			)
		}
	case "pyproject.toml":
		if err := e.pythonCli.InstallProject(
			ctx, projectDir, venvName, envVars,
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
	execCtx tools.ExecutionContext,
) (exec.RunResult, error) {
	pyCmd := e.resolvePythonPath()

	runArgs := exec.
		NewRunArgs(pyCmd, scriptPath).
		WithEnv(execCtx.EnvVars)

	// Prefer configured cwd; fall back to script's directory.
	cwd := execCtx.Cwd
	if cwd == "" {
		cwd = filepath.Dir(scriptPath)
	}
	runArgs = runArgs.WithCwd(cwd)

	if execCtx.Interactive != nil {
		runArgs = runArgs.WithInteractive(
			*execCtx.Interactive,
		)
	}
	if execCtx.StdOut != nil {
		runArgs = runArgs.WithStdOut(execCtx.StdOut)
	}

	return e.commandRunner.Run(ctx, runArgs)
}

// Cleanup is a no-op for the Python executor — no temporary
// resources are created during Prepare.
func (e *pythonExecutor) Cleanup(_ context.Context) error {
	return nil
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

// detectExistingVenv checks for an active or pre-existing virtual
// environment. It inspects the VIRTUAL_ENV environment variable
// first, then looks for well-known venv directory names (.venv,
// venv) inside the project directory. Returns the venv directory
// path or empty string when none is found.
func detectExistingVenv(
	execCtx tools.ExecutionContext,
	projCtx *ProjectContext,
) string {
	// 1. VIRTUAL_ENV from the process environment.
	if venv := envVarValue(
		execCtx.EnvVars, "VIRTUAL_ENV",
	); venv != "" {
		info, err := os.Stat(venv)
		if err == nil && info.IsDir() {
			return venv
		}
	}

	// 2. Well-known venv directories in the project path.
	if projCtx == nil {
		return ""
	}
	for _, name := range []string{".venv", "venv"} {
		candidate := filepath.Join(
			projCtx.ProjectDir, name,
		)
		if hasPyvenvCfg(candidate) {
			return candidate
		}
	}
	return ""
}

// hasPyvenvCfg returns true when dir contains a pyvenv.cfg file,
// indicating it is a Python virtual environment.
func hasPyvenvCfg(dir string) bool {
	info, err := os.Stat(
		filepath.Join(dir, "pyvenv.cfg"),
	)
	return err == nil && !info.IsDir()
}

// envVarValue extracts the value of a KEY=value pair from a
// slice of environment strings. Returns empty string if not
// found.
func envVarValue(envVars []string, key string) string {
	prefix := key + "="
	for _, v := range envVars {
		if strings.HasPrefix(v, prefix) {
			return v[len(prefix):]
		}
	}
	return ""
}

// relativeVenvName computes the relative directory name of
// venvPath within projectDir. Returns the name and true when
// the venv is a direct child of projectDir; returns ("", false)
// when the venv is outside projectDir or at the root itself.
func relativeVenvName(
	projectDir, venvPath string,
) (string, bool) {
	rel, err := filepath.Rel(projectDir, venvPath)
	if err != nil ||
		strings.HasPrefix(rel, "..") || rel == "." {
		return "", false
	}
	return rel, true
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

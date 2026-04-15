// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package language

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/python"
)

// pythonHookConfig holds the executor-specific configuration
// deserialized from the hook's "config" property bag.
type pythonHookConfig struct {
	// VirtualEnvName overrides the auto-detected virtual
	// environment directory name (e.g. ".venv", "my_env").
	// Must be a plain directory name with no path separators.
	VirtualEnvName string `json:"virtualEnvName"`
}

// pythonTools abstracts the Python CLI operations needed by
// pythonExecutor, decoupling it from the concrete [python.Cli]
// for testability. [python.Cli] satisfies this interface.
type pythonTools interface {
	CheckInstalled(ctx context.Context) error
	EnsureVirtualEnv(
		ctx context.Context,
		workingDir, name string,
		env []string,
	) error
	InstallDependencies(
		ctx context.Context,
		dir, venvName, depFile string,
		env []string,
	) error
	ResolveCommand() (string, error)
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
// [python.VenvNameForDir]: {projectDirName}_env.
//
// An explicit virtualEnvName in the hook config always takes
// precedence over auto-detection. When no config override is
// set, Prepare checks for an existing virtual environment —
// either via the VIRTUAL_ENV environment variable or well-known
// directory names (.venv, venv) in the project path. When an
// existing venv is detected, creation is skipped but dependency
// installation still runs when a Python project file is present
// and the venv is inside the project.
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

	// 3. Parse config early — an explicit virtualEnvName
	//    takes priority over auto-detected venvs.
	cfg, err := tools.UnmarshalHookConfig[pythonHookConfig](
		execCtx.Config,
	)
	if err != nil {
		return fmt.Errorf(
			"parsing python hook config: %w", err,
		)
	}

	hasConfigVenv := cfg.VirtualEnvName != ""
	if hasConfigVenv {
		if err := validateVenvName(
			cfg.VirtualEnvName,
		); err != nil {
			return err
		}
	}

	// 4. Detect existing virtual environment (VIRTUAL_ENV
	//    env var or well-known directories). Skip when
	//    config explicitly specifies a venv name — the
	//    user's override should always win.
	if !hasConfigVenv {
		if existing := detectExistingVenv(
			execCtx, projCtx,
		); existing != "" {
			e.venvPath = existing

			// Still install deps when a Python project
			// file exists and the venv lives inside
			// projectDir (so the python CLI can locate
			// the activation script).
			if projCtx != nil &&
				projCtx.Language == HookKindPython {
				if name, ok := relativeVenvName(
					projCtx.ProjectDir, existing,
				); ok {
					depFile := filepath.Base(
						projCtx.DependencyFile,
					)
					if err := e.pythonCli.InstallDependencies(
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
	}

	// No project file — run with system Python directly.
	if projCtx == nil {
		return nil
	}

	// Skip venv setup if the discovered project is not
	// Python (e.g. a package.json for JS living near the
	// Python script).
	if projCtx.Language != HookKindPython {
		return nil
	}

	// 5. Resolve virtual environment name. Use the
	//    config value if provided, otherwise fall back
	//    to the directory-based default.
	var venvName string
	if hasConfigVenv {
		venvName = cfg.VirtualEnvName
	} else {
		venvName = python.VenvNameForDir(
			projCtx.ProjectDir,
		)
	}

	if err := e.pythonCli.EnsureVirtualEnv(
		ctx, projCtx.ProjectDir,
		venvName, execCtx.EnvVars,
	); err != nil {
		return err
	}

	// 6. Install dependencies from the discovered file.
	depFile := filepath.Base(projCtx.DependencyFile)
	if err := e.pythonCli.InstallDependencies(
		ctx, projCtx.ProjectDir,
		venvName, depFile, execCtx.EnvVars,
	); err != nil {
		return err
	}

	e.venvPath = filepath.Join(
		projCtx.ProjectDir, venvName,
	)
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
// returns the venv's Python binary via [python.VenvPythonPath];
// otherwise it falls back to the system-level Python command
// via [pythonTools.ResolveCommand].
func (e *pythonExecutor) resolvePythonPath() string {
	if e.venvPath != "" {
		return python.VenvPythonPath(e.venvPath)
	}
	cmd, err := e.pythonCli.ResolveCommand()
	if err != nil {
		// Prepare() validates Python is installed, so
		// this should be unreachable. Fall back to the
		// platform-conventional command name.
		if runtime.GOOS == "windows" {
			return "python"
		}
		return "python3"
	}
	return cmd
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

// validateVenvName checks that the user-supplied virtual
// environment name is a plain directory name — no path
// separators or traversal components are allowed.
func validateVenvName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf(
			"virtualEnvName must not be empty",
		)
	}
	if name == "." || name == ".." {
		return fmt.Errorf(
			"virtualEnvName %q is not a valid "+
				"directory name", name,
		)
	}
	if strings.ContainsAny(name, "/\\") ||
		strings.ContainsRune(name, os.PathSeparator) {
		return fmt.Errorf(
			"virtualEnvName %q must not contain "+
				"path separators", name,
		)
	}
	return nil
}

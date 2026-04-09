// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package language

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// mockPythonTools — test double for the pythonTools interface
// ---------------------------------------------------------------------------

type mockPythonTools struct {
	checkInstalledErr error
	createVenvErr     error
	installReqErr     error
	installProjErr    error

	createVenvCalled  bool
	installReqCalled  bool
	installProjCalled bool

	venvDir  string // CreateVirtualEnv workingDir
	venvName string // CreateVirtualEnv name
	reqDir   string // InstallRequirements workingDir
	reqVenv  string // InstallRequirements environment
	reqFile  string // InstallRequirements requirementFile
	projDir  string // InstallProject workingDir
	projVenv string // InstallProject environment
}

func (m *mockPythonTools) CheckInstalled(
	_ context.Context,
) error {
	return m.checkInstalledErr
}

func (m *mockPythonTools) CreateVirtualEnv(
	_ context.Context,
	workingDir, name string,
	_ []string,
) error {
	m.createVenvCalled = true
	m.venvDir = workingDir
	m.venvName = name
	return m.createVenvErr
}

func (m *mockPythonTools) InstallRequirements(
	_ context.Context,
	workingDir, environment, requirementFile string,
	_ []string,
) error {
	m.installReqCalled = true
	m.reqDir = workingDir
	m.reqVenv = environment
	m.reqFile = requirementFile
	return m.installReqErr
}

func (m *mockPythonTools) InstallProject(
	_ context.Context,
	workingDir, environment string,
	_ []string,
) error {
	m.installProjCalled = true
	m.projDir = workingDir
	m.projVenv = environment
	return m.installProjErr
}

// mockCommandRunner is a minimal mock of [exec.CommandRunner]
// used to construct test dependencies without invoking real
// processes.
type mockCommandRunner struct {
	lastRunArgs  exec.RunArgs
	runResult    exec.RunResult
	runErr       error
	toolInPathFn func(name string) error
}

func (m *mockCommandRunner) Run(
	_ context.Context,
	args exec.RunArgs,
) (exec.RunResult, error) {
	m.lastRunArgs = args
	return m.runResult, m.runErr
}

func (m *mockCommandRunner) RunList(
	_ context.Context,
	_ []string,
	_ exec.RunArgs,
) (exec.RunResult, error) {
	return m.runResult, m.runErr
}

func (m *mockCommandRunner) ToolInPath(name string) error {
	if m.toolInPathFn != nil {
		return m.toolInPathFn(name)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Prepare tests
// ---------------------------------------------------------------------------

func TestPythonPrepare_PythonNotInstalled(t *testing.T) {
	cli := &mockPythonTools{
		checkInstalledErr: errors.New("python not found"),
	}
	e := newPythonExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	execCtx := tools.ExecutionContext{BoundaryDir: t.TempDir()}
	err := e.Prepare(t.Context(), "/any/hook.py", execCtx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "python 3 is required")
	assert.ErrorIs(t, err, cli.checkInstalledErr)
	assert.False(t, cli.createVenvCalled)
	assert.False(t, cli.installReqCalled)
}

func TestPythonPrepare_NoProjectFile(t *testing.T) {
	dir := t.TempDir()
	cli := &mockPythonTools{}
	e := newPythonExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	execCtx := tools.ExecutionContext{
		BoundaryDir: dir,
		Cwd:         dir,
	}
	scriptPath := filepath.Join(dir, "hook.py")
	err := e.Prepare(t.Context(), scriptPath, execCtx)

	require.NoError(t, err)
	assert.False(t, cli.createVenvCalled)
	assert.False(t, cli.installReqCalled)
	assert.False(t, cli.installProjCalled)
	assert.Empty(t, e.venvPath)
}

func TestPythonPrepare_WithRequirementsTxt(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	hooksDir := filepath.Join(projectDir, "hooks")
	require.NoError(t, os.MkdirAll(hooksDir, 0o700))
	writeFile(
		t,
		filepath.Join(projectDir, "requirements.txt"),
		"flask\n",
	)

	cli := &mockPythonTools{}
	e := newPythonExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	execCtx := tools.ExecutionContext{BoundaryDir: root}
	scriptPath := filepath.Join(hooksDir, "deploy.py")
	err := e.Prepare(t.Context(), scriptPath, execCtx)

	require.NoError(t, err)

	// Virtual environment should be created.
	assert.True(t, cli.createVenvCalled)
	assert.Equal(t, projectDir, cli.venvDir)
	assert.Equal(t, "myproject_env", cli.venvName)

	// Requirements should be installed.
	assert.True(t, cli.installReqCalled)
	assert.Equal(t, projectDir, cli.reqDir)
	assert.Equal(t, "myproject_env", cli.reqVenv)
	assert.Equal(t, "requirements.txt", cli.reqFile)

	// pyproject.toml path should NOT be used.
	assert.False(t, cli.installProjCalled)

	// venvPath should be recorded.
	expected := filepath.Join(projectDir, "myproject_env")
	assert.Equal(t, expected, e.venvPath)
}

func TestPythonPrepare_WithPyprojectToml(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	require.NoError(t, os.MkdirAll(projectDir, 0o700))
	writeFile(
		t,
		filepath.Join(projectDir, "pyproject.toml"),
		"[project]\nname = \"demo\"\n",
	)

	cli := &mockPythonTools{}
	e := newPythonExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	execCtx := tools.ExecutionContext{BoundaryDir: root}
	scriptPath := filepath.Join(projectDir, "deploy.py")
	err := e.Prepare(t.Context(), scriptPath, execCtx)

	require.NoError(t, err)

	assert.True(t, cli.createVenvCalled)
	assert.Equal(t, "myproject_env", cli.venvName)

	assert.True(t, cli.installProjCalled)
	assert.Equal(t, projectDir, cli.projDir)
	assert.Equal(t, "myproject_env", cli.projVenv)

	assert.False(t, cli.installReqCalled)
}

func TestPythonPrepare_VenvAlreadyExists(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	require.NoError(t, os.MkdirAll(projectDir, 0o700))
	writeFile(
		t,
		filepath.Join(projectDir, "requirements.txt"),
		"flask\n",
	)

	// Pre-create the venv directory to simulate an existing venv.
	venvDir := filepath.Join(projectDir, "myproject_env")
	require.NoError(t, os.MkdirAll(venvDir, 0o700))

	cli := &mockPythonTools{}
	e := newPythonExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	execCtx := tools.ExecutionContext{BoundaryDir: root}
	scriptPath := filepath.Join(projectDir, "deploy.py")
	err := e.Prepare(t.Context(), scriptPath, execCtx)

	require.NoError(t, err)
	assert.False(
		t, cli.createVenvCalled,
		"should skip venv creation when directory exists",
	)
	assert.True(
		t, cli.installReqCalled,
		"should still install requirements",
	)
	assert.NotEmpty(t, e.venvPath)
}

// ---------------------------------------------------------------------------
// Execute tests
// ---------------------------------------------------------------------------

func TestPythonExecute_WithVenv(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	hooksDir := filepath.Join(projectDir, "hooks")
	require.NoError(t, os.MkdirAll(hooksDir, 0o700))
	writeFile(
		t,
		filepath.Join(projectDir, "requirements.txt"),
		"flask\n",
	)

	cli := &mockPythonTools{}
	runner := &mockCommandRunner{}
	e := newPythonExecutorInternal(runner, cli)

	execCtx := tools.ExecutionContext{
		BoundaryDir: root,
		Cwd:         projectDir,
	}

	scriptPath := filepath.Join(hooksDir, "deploy.py")
	require.NoError(t, e.Prepare(t.Context(), scriptPath, execCtx))

	_, err := e.Execute(
		t.Context(), scriptPath, execCtx,
	)
	require.NoError(t, err)

	// The command should use the venv's Python binary.
	venvBase := filepath.Join(projectDir, "myproject_env")
	if runtime.GOOS == "windows" {
		assert.Equal(t,
			filepath.Join(
				venvBase, "Scripts", "python.exe",
			),
			runner.lastRunArgs.Cmd,
		)
	} else {
		assert.Equal(t,
			filepath.Join(venvBase, "bin", "python"),
			runner.lastRunArgs.Cmd,
		)
	}

	// Script path should be passed as an argument.
	require.Len(t, runner.lastRunArgs.Args, 1)
	assert.Equal(t, scriptPath, runner.lastRunArgs.Args[0])
}

func TestPythonExecute_WithoutVenv(t *testing.T) {
	dir := t.TempDir()
	runner := &mockCommandRunner{}
	e := newPythonExecutorInternal(runner, &mockPythonTools{})

	execCtx := tools.ExecutionContext{BoundaryDir: dir}
	scriptPath := filepath.Join(dir, "hook.py")
	_, err := e.Execute(
		t.Context(), scriptPath, execCtx,
	)
	require.NoError(t, err)

	// With no venv, system Python should be used.
	if runtime.GOOS == "windows" {
		// Default mock returns nil for ToolInPath → "py".
		assert.Equal(t, "py", runner.lastRunArgs.Cmd)
	} else {
		assert.Equal(t, "python3", runner.lastRunArgs.Cmd)
	}
}

func TestPythonExecute_EnvVarsPassthrough(t *testing.T) {
	runner := &mockCommandRunner{}
	envVars := []string{"FOO=bar", "BAZ=qux"}
	e := newPythonExecutorInternal(runner, &mockPythonTools{})

	execCtx := tools.ExecutionContext{
		BoundaryDir: t.TempDir(),
		EnvVars:     envVars,
	}
	scriptPath := filepath.Join(t.TempDir(), "hook.py")
	_, err := e.Execute(
		t.Context(), scriptPath, execCtx,
	)
	require.NoError(t, err)

	assert.Equal(t, envVars, runner.lastRunArgs.Env)
}

func TestPythonExecute_InteractiveMode(t *testing.T) {
	runner := &mockCommandRunner{}
	e := newPythonExecutorInternal(runner, &mockPythonTools{})

	execCtx := tools.ExecutionContext{
		BoundaryDir: t.TempDir(),
		Interactive: new(true),
	}
	scriptPath := filepath.Join(t.TempDir(), "hook.py")
	_, err := e.Execute(
		t.Context(), scriptPath, execCtx,
	)
	require.NoError(t, err)

	assert.True(t, runner.lastRunArgs.Interactive)
}

func TestPythonExecute_WorkingDirectory(t *testing.T) {
	t.Run("ConfiguredCwd", func(t *testing.T) {
		customCwd := filepath.Join(t.TempDir(), "custom")
		require.NoError(t, os.MkdirAll(customCwd, 0o700))

		runner := &mockCommandRunner{}
		e := newPythonExecutorInternal(runner, &mockPythonTools{})

		execCtx := tools.ExecutionContext{
			BoundaryDir: t.TempDir(),
			Cwd:         customCwd,
		}
		scriptPath := filepath.Join(t.TempDir(), "hook.py")
		_, err := e.Execute(
			t.Context(), scriptPath, execCtx,
		)
		require.NoError(t, err)

		assert.Equal(t, customCwd, runner.lastRunArgs.Cwd)
	})

	t.Run("FallbackToScriptDir", func(t *testing.T) {
		runner := &mockCommandRunner{}
		e := newPythonExecutorInternal(runner, &mockPythonTools{})

		execCtx := tools.ExecutionContext{
			BoundaryDir: t.TempDir(),
			// Cwd intentionally empty
		}
		scriptDir := filepath.Join(t.TempDir(), "scripts")
		scriptPath := filepath.Join(scriptDir, "hook.py")
		_, err := e.Execute(
			t.Context(), scriptPath, execCtx,
		)
		require.NoError(t, err)

		assert.Equal(t, scriptDir, runner.lastRunArgs.Cwd)
	})
}

// ---------------------------------------------------------------------------
// Existing venv detection tests
// ---------------------------------------------------------------------------

func TestPythonPrepare_VirtualEnvEnvVar(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	require.NoError(t, os.MkdirAll(projectDir, 0o700))
	writeFile(
		t,
		filepath.Join(projectDir, "requirements.txt"),
		"flask\n",
	)

	// Create a venv outside the project tree and add
	// pyvenv.cfg so it looks like a real venv.
	externalVenv := filepath.Join(root, "shared-venv")
	require.NoError(t, os.MkdirAll(externalVenv, 0o700))
	writeFile(
		t,
		filepath.Join(externalVenv, "pyvenv.cfg"),
		"home = /usr/bin\n",
	)

	cli := &mockPythonTools{}
	e := newPythonExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	execCtx := tools.ExecutionContext{
		BoundaryDir: root,
		EnvVars: []string{
			"VIRTUAL_ENV=" + externalVenv,
		},
	}
	scriptPath := filepath.Join(projectDir, "hook.py")
	err := e.Prepare(t.Context(), scriptPath, execCtx)

	require.NoError(t, err)
	assert.Equal(t, externalVenv, e.venvPath,
		"should use VIRTUAL_ENV path")
	assert.False(t, cli.createVenvCalled,
		"should skip venv creation")
	// External venv → dep installation is skipped because
	// the venv is outside the project directory.
	assert.False(t, cli.installReqCalled,
		"should skip dep install for external venv")
}

func TestPythonPrepare_VirtualEnvInsideProject(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	require.NoError(t, os.MkdirAll(projectDir, 0o700))
	writeFile(
		t,
		filepath.Join(projectDir, "requirements.txt"),
		"flask\n",
	)

	// Create a venv inside the project.
	localVenv := filepath.Join(projectDir, "my_env")
	require.NoError(t, os.MkdirAll(localVenv, 0o700))
	writeFile(
		t,
		filepath.Join(localVenv, "pyvenv.cfg"),
		"home = /usr/bin\n",
	)

	cli := &mockPythonTools{}
	e := newPythonExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	execCtx := tools.ExecutionContext{
		BoundaryDir: root,
		EnvVars: []string{
			"VIRTUAL_ENV=" + localVenv,
		},
	}
	scriptPath := filepath.Join(projectDir, "hook.py")
	err := e.Prepare(t.Context(), scriptPath, execCtx)

	require.NoError(t, err)
	assert.Equal(t, localVenv, e.venvPath)
	assert.False(t, cli.createVenvCalled,
		"should skip venv creation")
	assert.True(t, cli.installReqCalled,
		"should install deps for local venv")
	assert.Equal(t, "my_env", cli.reqVenv,
		"should pass relative venv name")
}

func TestPythonPrepare_DotVenvDirectory(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	require.NoError(t, os.MkdirAll(projectDir, 0o700))
	writeFile(
		t,
		filepath.Join(projectDir, "requirements.txt"),
		"flask\n",
	)

	// Create a .venv directory with pyvenv.cfg.
	dotVenv := filepath.Join(projectDir, ".venv")
	require.NoError(t, os.MkdirAll(dotVenv, 0o700))
	writeFile(
		t,
		filepath.Join(dotVenv, "pyvenv.cfg"),
		"home = /usr/bin\n",
	)

	cli := &mockPythonTools{}
	e := newPythonExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	execCtx := tools.ExecutionContext{BoundaryDir: root}
	scriptPath := filepath.Join(projectDir, "hook.py")
	err := e.Prepare(t.Context(), scriptPath, execCtx)

	require.NoError(t, err)
	assert.Equal(t, dotVenv, e.venvPath,
		"should detect .venv directory")
	assert.False(t, cli.createVenvCalled,
		"should skip venv creation")
	assert.True(t, cli.installReqCalled,
		"should still install requirements")
	assert.Equal(t, ".venv", cli.reqVenv,
		"should use .venv as venv name")
}

func TestPythonPrepare_VenvDirWithoutPyvenvCfg(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	require.NoError(t, os.MkdirAll(projectDir, 0o700))
	writeFile(
		t,
		filepath.Join(projectDir, "requirements.txt"),
		"flask\n",
	)

	// Create a .venv directory WITHOUT pyvenv.cfg — should
	// not be detected as an existing venv.
	dotVenv := filepath.Join(projectDir, ".venv")
	require.NoError(t, os.MkdirAll(dotVenv, 0o700))

	cli := &mockPythonTools{}
	e := newPythonExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	execCtx := tools.ExecutionContext{BoundaryDir: root}
	scriptPath := filepath.Join(projectDir, "hook.py")
	err := e.Prepare(t.Context(), scriptPath, execCtx)

	require.NoError(t, err)
	// Falls through to normal venv creation flow.
	assert.True(t, cli.createVenvCalled,
		"should create venv when .venv has no pyvenv.cfg")
	assert.Equal(t, "myproject_env", cli.venvName)
}

func TestPythonPrepare_VirtualEnvInvalid(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	require.NoError(t, os.MkdirAll(projectDir, 0o700))
	writeFile(
		t,
		filepath.Join(projectDir, "requirements.txt"),
		"flask\n",
	)

	cli := &mockPythonTools{}
	e := newPythonExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	// VIRTUAL_ENV points to a non-existent directory.
	execCtx := tools.ExecutionContext{
		BoundaryDir: root,
		EnvVars: []string{
			"VIRTUAL_ENV=/nonexistent/path",
		},
	}
	scriptPath := filepath.Join(projectDir, "hook.py")
	err := e.Prepare(t.Context(), scriptPath, execCtx)

	require.NoError(t, err)
	// Should fall through to normal venv creation.
	assert.True(t, cli.createVenvCalled,
		"should create venv when VIRTUAL_ENV is invalid")
}

func TestPythonPrepare_VirtualEnvNoProjectFile(t *testing.T) {
	dir := t.TempDir()

	// Create a venv directory.
	venvDir := filepath.Join(dir, "myvenv")
	require.NoError(t, os.MkdirAll(venvDir, 0o700))
	writeFile(
		t,
		filepath.Join(venvDir, "pyvenv.cfg"),
		"home = /usr/bin\n",
	)

	cli := &mockPythonTools{}
	e := newPythonExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	execCtx := tools.ExecutionContext{
		BoundaryDir: dir,
		EnvVars: []string{
			"VIRTUAL_ENV=" + venvDir,
		},
	}
	scriptPath := filepath.Join(dir, "hook.py")
	err := e.Prepare(t.Context(), scriptPath, execCtx)

	require.NoError(t, err)
	assert.Equal(t, venvDir, e.venvPath,
		"should use VIRTUAL_ENV even without project file")
	assert.False(t, cli.createVenvCalled)
	assert.False(t, cli.installReqCalled)
}

func TestPythonPrepare_VenvDirVenv(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	require.NoError(t, os.MkdirAll(projectDir, 0o700))
	writeFile(
		t,
		filepath.Join(projectDir, "pyproject.toml"),
		"[project]\nname = \"demo\"\n",
	)

	// Create "venv" directory (not ".venv") with pyvenv.cfg.
	venvDir := filepath.Join(projectDir, "venv")
	require.NoError(t, os.MkdirAll(venvDir, 0o700))
	writeFile(
		t,
		filepath.Join(venvDir, "pyvenv.cfg"),
		"home = /usr/bin\n",
	)

	cli := &mockPythonTools{}
	e := newPythonExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	execCtx := tools.ExecutionContext{BoundaryDir: root}
	scriptPath := filepath.Join(
		projectDir, "hook.py",
	)
	err := e.Prepare(t.Context(), scriptPath, execCtx)

	require.NoError(t, err)
	assert.Equal(t, venvDir, e.venvPath,
		"should detect venv directory")
	assert.False(t, cli.createVenvCalled)
	assert.True(t, cli.installProjCalled,
		"should install project deps")
	assert.Equal(t, "venv", cli.projVenv)
}

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
	ensureVenvErr     error
	installDepsErr    error
	resolveCommandCmd string
	resolveCommandErr error

	ensureVenvCalled  bool
	installDepsCalled bool

	ensureVenvDir  string // EnsureVirtualEnv workingDir
	ensureVenvName string // EnsureVirtualEnv name
	depsDir        string // InstallDependencies dir
	depsVenv       string // InstallDependencies venvName
	depsFile       string // InstallDependencies depFile
}

func (m *mockPythonTools) CheckInstalled(
	_ context.Context,
) error {
	return m.checkInstalledErr
}

func (m *mockPythonTools) EnsureVirtualEnv(
	_ context.Context,
	workingDir, name string,
	_ []string,
) error {
	m.ensureVenvCalled = true
	m.ensureVenvDir = workingDir
	m.ensureVenvName = name
	return m.ensureVenvErr
}

func (m *mockPythonTools) InstallDependencies(
	_ context.Context,
	dir, venvName, depFile string,
	_ []string,
) error {
	m.installDepsCalled = true
	m.depsDir = dir
	m.depsVenv = venvName
	m.depsFile = depFile
	return m.installDepsErr
}

func (m *mockPythonTools) ResolveCommand() (string, error) {
	if m.resolveCommandErr != nil {
		return "", m.resolveCommandErr
	}
	if m.resolveCommandCmd != "" {
		return m.resolveCommandCmd, nil
	}
	if runtime.GOOS == "windows" {
		return "py", nil
	}
	return "python3", nil
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
	assert.False(t, cli.ensureVenvCalled)
	assert.False(t, cli.installDepsCalled)
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
	assert.False(t, cli.ensureVenvCalled)
	assert.False(t, cli.installDepsCalled)
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

	// Virtual environment should be ensured.
	assert.True(t, cli.ensureVenvCalled)
	assert.Equal(t, projectDir, cli.ensureVenvDir)
	assert.Equal(t, "myproject_env", cli.ensureVenvName)

	// Requirements should be installed.
	assert.True(t, cli.installDepsCalled)
	assert.Equal(t, projectDir, cli.depsDir)
	assert.Equal(t, "myproject_env", cli.depsVenv)
	assert.Equal(t, "requirements.txt", cli.depsFile)

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

	assert.True(t, cli.ensureVenvCalled)
	assert.Equal(t, "myproject_env", cli.ensureVenvName)

	assert.True(t, cli.installDepsCalled)
	assert.Equal(t, projectDir, cli.depsDir)
	assert.Equal(t, "myproject_env", cli.depsVenv)
	assert.Equal(t, "pyproject.toml", cli.depsFile)
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
	// EnsureVirtualEnv is called — the shared helper
	// handles skipping creation when the dir exists.
	assert.True(t, cli.ensureVenvCalled)
	assert.True(
		t, cli.installDepsCalled,
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
		// Default mock returns "py" via ResolveCommand.
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
	assert.False(t, cli.ensureVenvCalled,
		"should skip venv ensure")
	// External venv → dep installation is skipped because
	// the venv is outside the project directory.
	assert.False(t, cli.installDepsCalled,
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
	assert.False(t, cli.ensureVenvCalled,
		"should skip venv ensure")
	assert.True(t, cli.installDepsCalled,
		"should install deps for local venv")
	assert.Equal(t, "my_env", cli.depsVenv,
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
	assert.False(t, cli.ensureVenvCalled,
		"should skip venv ensure")
	assert.True(t, cli.installDepsCalled,
		"should still install requirements")
	assert.Equal(t, ".venv", cli.depsVenv,
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
	// Falls through to normal venv ensure flow.
	assert.True(t, cli.ensureVenvCalled,
		"should ensure venv when .venv has no pyvenv.cfg")
	assert.Equal(t, "myproject_env", cli.ensureVenvName)
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
	// Should fall through to normal venv ensure.
	assert.True(t, cli.ensureVenvCalled,
		"should ensure venv when VIRTUAL_ENV is invalid")
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
	assert.False(t, cli.ensureVenvCalled)
	assert.False(t, cli.installDepsCalled)
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
	assert.False(t, cli.ensureVenvCalled)
	assert.True(t, cli.installDepsCalled,
		"should install project deps")
	assert.Equal(t, "venv", cli.depsVenv)
	assert.Equal(t, "pyproject.toml", cli.depsFile)
}

// ---------------------------------------------------------------------------
// virtualEnvName config tests
// ---------------------------------------------------------------------------

func TestPythonPrepare_VirtualEnvNameConfig(t *testing.T) {
	tests := []struct {
		name         string
		config       map[string]any
		wantVenvName string // expected EnsureVirtualEnv name
		wantErr      string // substring of error, empty = no error
	}{
		{
			name:         "DotVenvName",
			config:       map[string]any{"virtualEnvName": ".venv"},
			wantVenvName: ".venv",
		},
		{
			name:         "CustomName",
			config:       map[string]any{"virtualEnvName": "my_env"},
			wantVenvName: "my_env",
		},
		{
			name:         "NoConfig_FallsBackToAutoDetect",
			config:       nil,
			wantVenvName: "myproject_env",
		},
		{
			name:         "EmptyConfig_FallsBackToAutoDetect",
			config:       map[string]any{},
			wantVenvName: "myproject_env",
		},
		{
			name:    "EmptyString_Rejected",
			config:  map[string]any{"virtualEnvName": "   "},
			wantErr: "virtualEnvName must not be empty",
		},
		{
			name:    "PathTraversal_Rejected",
			config:  map[string]any{"virtualEnvName": "../evil"},
			wantErr: "must not contain path separators",
		},
		{
			name:    "ForwardSlash_Rejected",
			config:  map[string]any{"virtualEnvName": "foo/bar"},
			wantErr: "must not contain path separators",
		},
		{
			name:    "BackSlash_Rejected",
			config:  map[string]any{"virtualEnvName": `foo\bar`},
			wantErr: "must not contain path separators",
		},
		{
			name:    "Dot_Rejected",
			config:  map[string]any{"virtualEnvName": "."},
			wantErr: "is not a valid directory name",
		},
		{
			name:    "DotDot_Rejected",
			config:  map[string]any{"virtualEnvName": ".."},
			wantErr: "is not a valid directory name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			projectDir := filepath.Join(
				root, "myproject",
			)
			hooksDir := filepath.Join(
				projectDir, "hooks",
			)
			require.NoError(t,
				os.MkdirAll(hooksDir, 0o700),
			)
			writeFile(t,
				filepath.Join(
					projectDir, "requirements.txt",
				),
				"flask\n",
			)

			cli := &mockPythonTools{}
			e := newPythonExecutorInternal(
				&mockCommandRunner{}, cli,
			)

			execCtx := tools.ExecutionContext{
				BoundaryDir: root,
				Config:      tt.config,
			}
			scriptPath := filepath.Join(
				hooksDir, "deploy.py",
			)
			err := e.Prepare(
				t.Context(), scriptPath, execCtx,
			)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.False(t, cli.ensureVenvCalled,
					"should not create venv on error",
				)
				return
			}

			require.NoError(t, err)
			assert.True(t, cli.ensureVenvCalled)
			assert.Equal(t,
				tt.wantVenvName, cli.ensureVenvName,
			)
			assert.True(t, cli.installDepsCalled)
			assert.Equal(t,
				tt.wantVenvName, cli.depsVenv,
			)
			assert.Equal(t,
				"requirements.txt", cli.depsFile,
			)

			expectedPath := filepath.Join(
				projectDir, tt.wantVenvName,
			)
			assert.Equal(t, expectedPath, e.venvPath)
		})
	}
}

func TestValidateVenvName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"ValidDotVenv", ".venv", false},
		{"ValidCustom", "my_env", false},
		{"ValidWithDots", ".my.env", false},
		{"Empty", "", true},
		{"Whitespace", "   ", true},
		{"Dot", ".", true},
		{"DotDot", "..", true},
		{"ForwardSlash", "foo/bar", true},
		{"BackSlash", `foo\bar`, true},
		{"TrailingSlash", "venv/", true},
		{"LeadingSlash", "/venv", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVenvName(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

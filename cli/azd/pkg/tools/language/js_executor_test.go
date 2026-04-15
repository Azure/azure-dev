// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package language

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// JavaScript executor unit tests
// ---------------------------------------------------------------------------

func TestJsExecute_HappyPath(t *testing.T) {
	runner := &mockCommandRunner{}
	e := newJSExecutorInternal(runner, &mockNodeTools{})

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "hook.js")

	execCtx := tools.ExecutionContext{
		BoundaryDir: dir,
		Cwd:         dir,
		EnvVars:     []string{"A=1"},
	}

	_, err := e.Execute(t.Context(), scriptPath, execCtx)
	require.NoError(t, err)

	assert.Equal(t, "node", runner.lastRunArgs.Cmd)
	require.Len(t, runner.lastRunArgs.Args, 1)
	assert.Equal(t, scriptPath, runner.lastRunArgs.Args[0])
	assert.Equal(t, dir, runner.lastRunArgs.Cwd)
	assert.Equal(t, []string{"A=1"}, runner.lastRunArgs.Env)
}

func TestJsExecute_FallbackCwd(t *testing.T) {
	runner := &mockCommandRunner{}
	e := newJSExecutorInternal(runner, &mockNodeTools{})

	scriptDir := filepath.Join(t.TempDir(), "hooks")
	scriptPath := filepath.Join(scriptDir, "hook.js")

	execCtx := tools.ExecutionContext{
		BoundaryDir: t.TempDir(),
		// Cwd intentionally empty
	}

	_, err := e.Execute(t.Context(), scriptPath, execCtx)
	require.NoError(t, err)

	assert.Equal(t, scriptDir, runner.lastRunArgs.Cwd,
		"should fall back to script directory")
}

func TestJsExecute_InteractiveMode(t *testing.T) {
	runner := &mockCommandRunner{}
	e := newJSExecutorInternal(runner, &mockNodeTools{})

	interactive := true
	execCtx := tools.ExecutionContext{
		BoundaryDir: t.TempDir(),
		Interactive: &interactive,
	}
	scriptPath := filepath.Join(t.TempDir(), "hook.js")

	_, err := e.Execute(t.Context(), scriptPath, execCtx)
	require.NoError(t, err)

	assert.True(t, runner.lastRunArgs.Interactive)
}

func TestJsExecute_ScriptError(t *testing.T) {
	runner := &mockCommandRunner{
		runResult: exec.NewRunResult(1, "", "error output"),
		runErr:    errors.New("exit code 1"),
	}
	e := newJSExecutorInternal(runner, &mockNodeTools{})

	execCtx := tools.ExecutionContext{
		BoundaryDir: t.TempDir(),
	}
	scriptPath := filepath.Join(t.TempDir(), "hook.js")

	res, err := e.Execute(t.Context(), scriptPath, execCtx)
	require.Error(t, err)
	assert.Equal(t, 1, res.ExitCode)
}

func TestJsPrepare_WithPackageJSON(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	require.NoError(t, os.MkdirAll(projectDir, 0o700))
	writeFile(t,
		filepath.Join(projectDir, "package.json"),
		`{"name": "test"}`,
	)

	mock := &mockNodeTools{}
	e := newJSExecutorInternal(
		&mockCommandRunner{}, mock,
	)

	execCtx := tools.ExecutionContext{
		BoundaryDir: root,
	}
	scriptPath := filepath.Join(projectDir, "hook.js")

	err := e.Prepare(t.Context(), scriptPath, execCtx)
	require.NoError(t, err)
	assert.True(t, mock.installCalled)
}

func TestJsPrepare_NodeMissing(t *testing.T) {
	mock := &mockNodeTools{
		checkInstalledErr: errors.New("not found"),
	}
	e := newJSExecutorInternal(
		&mockCommandRunner{}, mock,
	)

	execCtx := tools.ExecutionContext{
		BoundaryDir: t.TempDir(),
	}

	err := e.Prepare(
		t.Context(), "/any/hook.js", execCtx,
	)
	require.Error(t, err)

	var sugErr *errorhandler.ErrorWithSuggestion
	require.ErrorAs(t, err, &sugErr)
	assert.Contains(t, sugErr.Message, "Node.js is required")
}

func TestJsCleanup_NoOp(t *testing.T) {
	e := newJSExecutorInternal(
		&mockCommandRunner{}, &mockNodeTools{},
	)
	require.NoError(t, e.Cleanup(t.Context()))
}

func TestJsPrepare_PackageManagerOverride(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	require.NoError(t, os.MkdirAll(projectDir, 0o700))
	writeFile(t,
		filepath.Join(projectDir, "package.json"),
		`{"name": "test"}`,
	)

	mock := &mockNodeTools{}
	runner := &mockCommandRunner{
		runResult: exec.NewRunResult(
			0, "v20.0.0", "",
		),
	}
	e := newJSExecutorInternal(runner, mock)

	execCtx := tools.ExecutionContext{
		BoundaryDir: root,
		Config: map[string]any{
			"packageManager": "yarn",
		},
	}
	scriptPath := filepath.Join(projectDir, "hook.js")

	err := e.Prepare(t.Context(), scriptPath, execCtx)
	require.NoError(t, err)

	// Default mock bypassed — config override used yarn.
	assert.False(t, mock.installCalled)
	assert.Equal(t, "yarn", runner.lastRunArgs.Cmd)
}

func TestJsPrepare_InvalidPackageManager(t *testing.T) {
	e := newJSExecutorInternal(
		&mockCommandRunner{}, &mockNodeTools{},
	)

	execCtx := tools.ExecutionContext{
		BoundaryDir: t.TempDir(),
		Config: map[string]any{
			"packageManager": "bun",
		},
	}

	err := e.Prepare(
		t.Context(), "/any/hook.js", execCtx,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(),
		`invalid packageManager config value "bun"`)
}

// ---------------------------------------------------------------------------
// Table-driven comprehensive tests
// ---------------------------------------------------------------------------

func TestJsExecutor_TableDriven(t *testing.T) {
	tests := []struct {
		name        string
		withPkgJSON bool
		nodeMissing bool
		installErr  error
		runErr      error
		runExitCode int
		wantPrepErr bool
		wantExecErr bool
	}{
		{
			name:        "StandaloneScript",
			withPkgJSON: false,
			wantPrepErr: false,
			wantExecErr: false,
		},
		{
			name:        "WithPackageJSON",
			withPkgJSON: true,
			wantPrepErr: false,
			wantExecErr: false,
		},
		{
			name:        "NodeMissing",
			nodeMissing: true,
			wantPrepErr: true,
		},
		{
			name:        "InstallFails",
			withPkgJSON: true,
			installErr:  errors.New("install failed"),
			wantPrepErr: true,
		},
		{
			name:        "ScriptNonZeroExit",
			runErr:      errors.New("exit 1"),
			runExitCode: 1,
			wantExecErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			projectDir := filepath.Join(root, "proj")
			require.NoError(t,
				os.MkdirAll(projectDir, 0o700),
			)

			if tt.withPkgJSON {
				writeFile(t,
					filepath.Join(
						projectDir, "package.json",
					),
					`{"name":"test"}`,
				)
			}

			var checkErr error
			if tt.nodeMissing {
				checkErr = errors.New("node not found")
			}

			mock := &mockNodeTools{
				checkInstalledErr: checkErr,
				installErr:        tt.installErr,
			}

			runner := &mockCommandRunner{
				runResult: exec.NewRunResult(
					tt.runExitCode, "", "",
				),
				runErr: tt.runErr,
			}

			e := newJSExecutorInternal(runner, mock)
			execCtx := tools.ExecutionContext{
				BoundaryDir: root,
			}
			scriptPath := filepath.Join(
				projectDir, "hook.js",
			)

			err := e.Prepare(
				t.Context(), scriptPath, execCtx,
			)
			if tt.wantPrepErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			_, err = e.Execute(
				t.Context(), scriptPath, execCtx,
			)
			if tt.wantExecErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

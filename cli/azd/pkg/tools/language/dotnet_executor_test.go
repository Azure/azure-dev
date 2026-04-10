// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package language

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// mockDotNetTools — test double for the dotnetTools interface
// ---------------------------------------------------------------------------

type mockDotNetTools struct {
	checkInstalledErr error
	sdkVersionResult  semver.Version
	sdkVersionErr     error
	restoreErr        error
	buildErr          error

	restoreCalled  bool
	restoreProject string
	buildCalled    bool
	buildProject   string
}

func (m *mockDotNetTools) CheckInstalled(
	_ context.Context,
) error {
	return m.checkInstalledErr
}

func (m *mockDotNetTools) SdkVersion(
	_ context.Context,
) (semver.Version, error) {
	return m.sdkVersionResult, m.sdkVersionErr
}

func (m *mockDotNetTools) Restore(
	_ context.Context,
	project string,
	_ []string,
) error {
	m.restoreCalled = true
	m.restoreProject = project
	return m.restoreErr
}

func (m *mockDotNetTools) Build(
	_ context.Context,
	project string, _ string,
	_ string, _ []string,
) error {
	m.buildCalled = true
	m.buildProject = project
	return m.buildErr
}

// ---------------------------------------------------------------------------
// Prepare tests — project mode
// ---------------------------------------------------------------------------

func TestDotNetPrepare_DotNetNotInstalled(t *testing.T) {
	cli := &mockDotNetTools{
		checkInstalledErr: errors.New("dotnet not found"),
	}
	e := newDotNetExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	execCtx := tools.ExecutionContext{
		BoundaryDir: t.TempDir(),
	}
	err := e.Prepare(
		t.Context(), "/any/hook.cs", execCtx,
	)

	require.Error(t, err)

	var sugErr *errorhandler.ErrorWithSuggestion
	require.ErrorAs(t, err, &sugErr)
	assert.Contains(t, sugErr.Message,
		".NET SDK is required")
	assert.NotEmpty(t, sugErr.Suggestion)
	assert.NotEmpty(t, sugErr.Links)
	assert.False(t, cli.restoreCalled)
	assert.False(t, cli.buildCalled)
}

func TestDotNetPrepare_WithCsproj(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	hooksDir := filepath.Join(projectDir, "hooks")
	require.NoError(t, os.MkdirAll(hooksDir, 0o700))
	writeFile(t,
		filepath.Join(projectDir, "MyProject.csproj"),
		"<Project Sdk=\"Microsoft.NET.Sdk\" />",
	)

	cli := &mockDotNetTools{}
	e := newDotNetExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	execCtx := tools.ExecutionContext{BoundaryDir: root}
	scriptPath := filepath.Join(hooksDir, "hook.cs")
	err := e.Prepare(t.Context(), scriptPath, execCtx)

	require.NoError(t, err)

	assert.True(t, cli.restoreCalled,
		"should run dotnet restore")
	assert.Contains(t, cli.restoreProject,
		"MyProject.csproj")
	assert.True(t, cli.buildCalled,
		"should run dotnet build")
	assert.Contains(t, cli.buildProject,
		"MyProject.csproj")
	assert.NotEmpty(t, e.projectPath,
		"should set projectPath for Execute")
}

func TestDotNetPrepare_RestoreFails(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	require.NoError(t, os.MkdirAll(projectDir, 0o700))
	writeFile(t,
		filepath.Join(projectDir, "App.csproj"),
		"<Project />",
	)

	cli := &mockDotNetTools{
		restoreErr: errors.New("restore failed"),
	}
	e := newDotNetExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	execCtx := tools.ExecutionContext{BoundaryDir: root}
	scriptPath := filepath.Join(projectDir, "hook.cs")
	err := e.Prepare(t.Context(), scriptPath, execCtx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "dotnet restore failed")
	assert.False(t, cli.buildCalled,
		"should not build when restore fails")
}

func TestDotNetPrepare_BuildFails(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	require.NoError(t, os.MkdirAll(projectDir, 0o700))
	writeFile(t,
		filepath.Join(projectDir, "App.csproj"),
		"<Project />",
	)

	cli := &mockDotNetTools{
		buildErr: errors.New("build failed"),
	}
	e := newDotNetExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	execCtx := tools.ExecutionContext{BoundaryDir: root}
	scriptPath := filepath.Join(projectDir, "hook.cs")
	err := e.Prepare(t.Context(), scriptPath, execCtx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "dotnet build failed")
	assert.True(t, cli.restoreCalled,
		"should restore before failing on build")
}

// ---------------------------------------------------------------------------
// Prepare tests — single-file mode
// ---------------------------------------------------------------------------

func TestDotNetPrepare_SingleFile_Net10(t *testing.T) {
	dir := t.TempDir()
	cli := &mockDotNetTools{
		sdkVersionResult: semver.Version{
			Major: 10, Minor: 0, Patch: 100,
		},
	}
	e := newDotNetExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	execCtx := tools.ExecutionContext{
		BoundaryDir: dir,
	}
	scriptPath := filepath.Join(dir, "hook.cs")
	err := e.Prepare(t.Context(), scriptPath, execCtx)

	require.NoError(t, err)
	assert.False(t, cli.restoreCalled,
		"single-file mode should not restore")
	assert.False(t, cli.buildCalled,
		"single-file mode should not build")
	assert.Empty(t, e.projectPath,
		"projectPath should be empty for single-file")
}

func TestDotNetPrepare_SingleFile_OldSdk(t *testing.T) {
	dir := t.TempDir()
	cli := &mockDotNetTools{
		sdkVersionResult: semver.Version{
			Major: 8, Minor: 0, Patch: 300,
		},
	}
	e := newDotNetExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	execCtx := tools.ExecutionContext{
		BoundaryDir: dir,
	}
	scriptPath := filepath.Join(dir, "hook.cs")
	err := e.Prepare(t.Context(), scriptPath, execCtx)

	require.Error(t, err)

	var sugErr *errorhandler.ErrorWithSuggestion
	require.ErrorAs(t, err, &sugErr)
	assert.Contains(t, sugErr.Message,
		"Single-file .cs hooks require")
	assert.Contains(t, sugErr.Suggestion,
		"Create a .csproj project file")
	assert.NotEmpty(t, sugErr.Links)
}

func TestDotNetPrepare_SingleFile_VersionDetectFails(
	t *testing.T,
) {
	dir := t.TempDir()
	cli := &mockDotNetTools{
		sdkVersionErr: errors.New("version parse error"),
	}
	e := newDotNetExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	execCtx := tools.ExecutionContext{
		BoundaryDir: dir,
	}
	scriptPath := filepath.Join(dir, "hook.cs")
	err := e.Prepare(t.Context(), scriptPath, execCtx)

	require.Error(t, err)
	assert.Contains(t, err.Error(),
		"detecting .NET SDK version")
}

func TestDotNetPrepare_AmbiguousProjectFiles(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	require.NoError(t, os.MkdirAll(projectDir, 0o700))
	writeFile(t,
		filepath.Join(projectDir, "App.csproj"),
		"<Project />",
	)
	writeFile(t,
		filepath.Join(projectDir, "Tests.csproj"),
		"<Project />",
	)

	cli := &mockDotNetTools{}
	e := newDotNetExecutorInternal(
		&mockCommandRunner{}, cli,
	)

	execCtx := tools.ExecutionContext{BoundaryDir: root}
	scriptPath := filepath.Join(projectDir, "hook.cs")
	err := e.Prepare(t.Context(), scriptPath, execCtx)

	require.Error(t, err)
	assert.Contains(t, err.Error(),
		"found 2 .NET project files")
	assert.Contains(t, err.Error(), "dir")
	assert.False(t, cli.restoreCalled,
		"should not restore when ambiguous")
}

// ---------------------------------------------------------------------------
// Execute tests
// ---------------------------------------------------------------------------

func TestDotNetExecute_ProjectMode(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	hooksDir := filepath.Join(projectDir, "hooks")
	require.NoError(t, os.MkdirAll(hooksDir, 0o700))
	writeFile(t,
		filepath.Join(projectDir, "App.csproj"),
		"<Project />",
	)

	cli := &mockDotNetTools{}
	runner := &mockCommandRunner{}
	e := newDotNetExecutorInternal(runner, cli)

	execCtx := tools.ExecutionContext{
		BoundaryDir: root,
		Cwd:         projectDir,
	}
	scriptPath := filepath.Join(hooksDir, "hook.cs")

	require.NoError(t,
		e.Prepare(t.Context(), scriptPath, execCtx),
	)

	_, err := e.Execute(
		t.Context(), scriptPath, execCtx,
	)
	require.NoError(t, err)

	assert.Equal(t, "dotnet", runner.lastRunArgs.Cmd)
	assert.Contains(t, runner.lastRunArgs.Args, "run")
	assert.Contains(t, runner.lastRunArgs.Args, "--project")
	assert.Contains(t, runner.lastRunArgs.Args, "--no-build",
		"should skip rebuild since Prepare already built")
	assert.Equal(t, projectDir, runner.lastRunArgs.Cwd)
}

func TestDotNetExecute_SingleFileMode(t *testing.T) {
	dir := t.TempDir()
	runner := &mockCommandRunner{}
	cli := &mockDotNetTools{
		sdkVersionResult: semver.Version{
			Major: 10, Minor: 0, Patch: 0,
		},
	}
	e := newDotNetExecutorInternal(runner, cli)

	scriptPath := filepath.Join(dir, "hook.cs")
	execCtx := tools.ExecutionContext{
		BoundaryDir: dir,
		Cwd:         dir,
		EnvVars:     []string{"FOO=bar"},
	}

	require.NoError(t,
		e.Prepare(t.Context(), scriptPath, execCtx),
	)

	_, err := e.Execute(
		t.Context(), scriptPath, execCtx,
	)
	require.NoError(t, err)

	assert.Equal(t, "dotnet", runner.lastRunArgs.Cmd)
	assert.Equal(t, []string{"run", scriptPath},
		runner.lastRunArgs.Args)
	assert.Equal(t, dir, runner.lastRunArgs.Cwd)
	// Should include dotnet env vars + user env vars.
	assert.Contains(t, runner.lastRunArgs.Env,
		"DOTNET_NOLOGO=1")
	assert.Contains(t, runner.lastRunArgs.Env, "FOO=bar")
}

func TestDotNetExecute_FallbackCwd(t *testing.T) {
	runner := &mockCommandRunner{}
	cli := &mockDotNetTools{
		sdkVersionResult: semver.Version{
			Major: 10, Minor: 0, Patch: 0,
		},
	}
	e := newDotNetExecutorInternal(runner, cli)

	scriptDir := filepath.Join(t.TempDir(), "hooks")
	scriptPath := filepath.Join(scriptDir, "hook.cs")

	execCtx := tools.ExecutionContext{
		BoundaryDir: t.TempDir(),
		// Cwd intentionally empty
	}

	require.NoError(t,
		e.Prepare(t.Context(), scriptPath, execCtx),
	)

	_, err := e.Execute(
		t.Context(), scriptPath, execCtx,
	)
	require.NoError(t, err)

	assert.Equal(t, scriptDir, runner.lastRunArgs.Cwd,
		"should fall back to script directory")
}

func TestDotNetExecute_InteractiveMode(t *testing.T) {
	runner := &mockCommandRunner{}
	cli := &mockDotNetTools{
		sdkVersionResult: semver.Version{
			Major: 10, Minor: 0, Patch: 0,
		},
	}
	e := newDotNetExecutorInternal(runner, cli)

	interactive := true
	execCtx := tools.ExecutionContext{
		BoundaryDir: t.TempDir(),
		Interactive: &interactive,
	}
	scriptPath := filepath.Join(t.TempDir(), "hook.cs")

	require.NoError(t,
		e.Prepare(t.Context(), scriptPath, execCtx),
	)

	_, err := e.Execute(
		t.Context(), scriptPath, execCtx,
	)
	require.NoError(t, err)

	assert.True(t, runner.lastRunArgs.Interactive)
}

func TestDotNetExecute_ScriptError(t *testing.T) {
	runner := &mockCommandRunner{
		runResult: exec.NewRunResult(1, "", "error output"),
		runErr:    errors.New("exit code 1"),
	}
	cli := &mockDotNetTools{
		sdkVersionResult: semver.Version{
			Major: 10, Minor: 0, Patch: 0,
		},
	}
	e := newDotNetExecutorInternal(runner, cli)

	execCtx := tools.ExecutionContext{
		BoundaryDir: t.TempDir(),
	}
	scriptPath := filepath.Join(t.TempDir(), "hook.cs")

	require.NoError(t,
		e.Prepare(t.Context(), scriptPath, execCtx),
	)

	res, err := e.Execute(
		t.Context(), scriptPath, execCtx,
	)
	require.Error(t, err)
	assert.Equal(t, 1, res.ExitCode)
}

func TestDotNetCleanup_NoOp(t *testing.T) {
	e := newDotNetExecutorInternal(
		&mockCommandRunner{}, &mockDotNetTools{},
	)
	require.NoError(t, e.Cleanup(t.Context()))
}

// ---------------------------------------------------------------------------
// Table-driven comprehensive tests
// ---------------------------------------------------------------------------

func TestDotNetExecutor_TableDriven(t *testing.T) {
	tests := []struct {
		name        string
		withCsproj  bool
		dotnetMiss  bool
		oldSdk      bool
		restoreErr  error
		buildErr    error
		runErr      error
		runExitCode int
		wantPrepErr bool
		wantExecErr bool
	}{
		{
			name:        "ProjectMode_HappyPath",
			withCsproj:  true,
			wantPrepErr: false,
			wantExecErr: false,
		},
		{
			name:        "SingleFile_Net10",
			withCsproj:  false,
			wantPrepErr: false,
			wantExecErr: false,
		},
		{
			name:        "DotNetMissing",
			dotnetMiss:  true,
			wantPrepErr: true,
		},
		{
			name:        "SingleFile_OldSdk",
			withCsproj:  false,
			oldSdk:      true,
			wantPrepErr: true,
		},
		{
			name:        "RestoreFails",
			withCsproj:  true,
			restoreErr:  errors.New("restore failed"),
			wantPrepErr: true,
		},
		{
			name:        "BuildFails",
			withCsproj:  true,
			buildErr:    errors.New("build failed"),
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

			if tt.withCsproj {
				writeFile(t,
					filepath.Join(
						projectDir, "App.csproj",
					),
					"<Project />",
				)
			}

			var checkErr error
			if tt.dotnetMiss {
				checkErr = errors.New("dotnet not found")
			}

			sdkVer := semver.Version{
				Major: 10, Minor: 0, Patch: 0,
			}
			if tt.oldSdk {
				sdkVer = semver.Version{
					Major: 8, Minor: 0, Patch: 300,
				}
			}

			cli := &mockDotNetTools{
				checkInstalledErr: checkErr,
				sdkVersionResult:  sdkVer,
				restoreErr:        tt.restoreErr,
				buildErr:          tt.buildErr,
			}

			runner := &mockCommandRunner{
				runResult: exec.NewRunResult(
					tt.runExitCode, "", "",
				),
				runErr: tt.runErr,
			}

			e := newDotNetExecutorInternal(runner, cli)
			execCtx := tools.ExecutionContext{
				BoundaryDir: root,
			}
			scriptPath := filepath.Join(
				projectDir, "hook.cs",
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

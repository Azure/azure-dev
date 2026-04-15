// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package language

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/node"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// mockNodeTools — test double for the nodeTools interface
// ---------------------------------------------------------------------------

type mockNodeTools struct {
	checkInstalledErr error
	installErr        error

	installCalled bool
	installDir    string
	installEnv    []string
}

func (m *mockNodeTools) CheckInstalled(
	_ context.Context,
) error {
	return m.checkInstalledErr
}

func (m *mockNodeTools) Install(
	_ context.Context,
	projectPath string,
	env []string,
) error {
	m.installCalled = true
	m.installDir = projectPath
	m.installEnv = env
	return m.installErr
}

// ---------------------------------------------------------------------------
// prepareNodeProject tests
// ---------------------------------------------------------------------------

func TestNodePrepare_NodeNotInstalled(t *testing.T) {
	mock := &mockNodeTools{
		checkInstalledErr: errors.New("node not found"),
	}

	execCtx := tools.ExecutionContext{
		BoundaryDir: t.TempDir(),
	}

	projCtx, err := prepareNodeProject(
		t.Context(), mock, &mockCommandRunner{},
		"/any/hook.js", execCtx,
	)

	require.Error(t, err)
	assert.Nil(t, projCtx)

	// Should be an ErrorWithSuggestion.
	var sugErr *errorhandler.ErrorWithSuggestion
	require.ErrorAs(t, err, &sugErr)
	assert.Contains(
		t, sugErr.Message, "Node.js is required",
	)
	assert.Contains(
		t, sugErr.Suggestion, "nodejs.org",
	)
	assert.False(t, mock.installCalled)
}

func TestNodePrepare_CheckInstalledSuggestionPassthrough(
	t *testing.T,
) {
	// When CheckInstalled already returns an ErrorWithSuggestion
	// (e.g. from middleware), prepareNodeProject must pass it
	// through without re-wrapping.
	origErr := &errorhandler.ErrorWithSuggestion{
		Err:        errors.New("Node.js 16.3.0 is too old"),
		Message:    "Node.js version is too old.",
		Suggestion: "Upgrade to Node.js 18.0.0 or later.",
		Links: []errorhandler.ErrorLink{{
			Title: "Download Node.js",
			URL:   "https://nodejs.org/en/download/",
		}},
	}
	mock := &mockNodeTools{checkInstalledErr: origErr}

	execCtx := tools.ExecutionContext{
		BoundaryDir: t.TempDir(),
	}

	projCtx, err := prepareNodeProject(
		t.Context(), mock, &mockCommandRunner{},
		"/any/hook.js", execCtx,
	)

	require.Error(t, err)
	assert.Nil(t, projCtx)

	// The returned error should be the SAME instance,
	// not a new wrapper.
	assert.Same(t, origErr, err,
		"ErrorWithSuggestion should be passed through")
	assert.False(t, mock.installCalled)
}

func TestNodePrepare_WithPackageJSON(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	hooksDir := filepath.Join(projectDir, "hooks")
	require.NoError(t, os.MkdirAll(hooksDir, 0o700))
	writeFile(
		t,
		filepath.Join(projectDir, "package.json"),
		`{"name": "test"}`,
	)

	mock := &mockNodeTools{}
	envVars := []string{"FOO=bar"}

	execCtx := tools.ExecutionContext{
		BoundaryDir: root,
		EnvVars:     envVars,
	}
	scriptPath := filepath.Join(hooksDir, "deploy.js")

	projCtx, err := prepareNodeProject(
		t.Context(), mock, &mockCommandRunner{},
		scriptPath, execCtx,
	)

	require.NoError(t, err)
	require.NotNil(t, projCtx)
	assert.Equal(t, projectDir, projCtx.ProjectDir)
	assert.True(t, mock.installCalled)
	assert.Equal(t, projectDir, mock.installDir)
	assert.Equal(t, envVars, mock.installEnv)
}

func TestNodePrepare_NoPackageJSON(t *testing.T) {
	dir := t.TempDir()
	mock := &mockNodeTools{}

	execCtx := tools.ExecutionContext{
		BoundaryDir: dir,
	}
	scriptPath := filepath.Join(dir, "hook.js")

	projCtx, err := prepareNodeProject(
		t.Context(), mock, &mockCommandRunner{},
		scriptPath, execCtx,
	)

	require.NoError(t, err)
	assert.Nil(t, projCtx)
	assert.False(t, mock.installCalled)
}

func TestNodePrepare_InstallFails(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	require.NoError(t, os.MkdirAll(projectDir, 0o700))
	writeFile(
		t,
		filepath.Join(projectDir, "package.json"),
		`{"name": "test"}`,
	)

	mock := &mockNodeTools{
		installErr: errors.New("npm install failed"),
	}

	execCtx := tools.ExecutionContext{
		BoundaryDir: root,
	}
	scriptPath := filepath.Join(projectDir, "hook.js")

	projCtx, err := prepareNodeProject(
		t.Context(), mock, &mockCommandRunner{},
		scriptPath, execCtx,
	)

	require.Error(t, err)
	assert.Nil(t, projCtx)
	assert.Contains(t, err.Error(), "installing Node.js dependencies")
	assert.True(t, mock.installCalled)
}

func TestNodePrepare_PythonProjectIgnored(t *testing.T) {
	// When only requirements.txt is present (no package.json),
	// the Node executor should not try to install anything.
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	require.NoError(t, os.MkdirAll(projectDir, 0o700))
	writeFile(
		t,
		filepath.Join(projectDir, "requirements.txt"),
		"flask\n",
	)

	mock := &mockNodeTools{}
	execCtx := tools.ExecutionContext{
		BoundaryDir: root,
	}
	scriptPath := filepath.Join(projectDir, "hook.js")

	projCtx, err := prepareNodeProject(
		t.Context(), mock, &mockCommandRunner{},
		scriptPath, execCtx,
	)

	require.NoError(t, err)
	assert.Nil(t, projCtx,
		"should not return a project context when only "+
			"Python files exist")
	assert.False(t, mock.installCalled,
		"should not install deps when only Python files exist")
}

func TestNodePrepare_MixedLanguageFindsPackageJSON(
	t *testing.T,
) {
	// When both requirements.txt and package.json exist in the
	// same directory, the Node executor should find and install
	// from package.json (not be shadowed by Python priority).
	root := t.TempDir()
	projectDir := filepath.Join(root, "myproject")
	require.NoError(t, os.MkdirAll(projectDir, 0o700))
	writeFile(
		t,
		filepath.Join(projectDir, "requirements.txt"),
		"flask\n",
	)
	writeFile(
		t,
		filepath.Join(projectDir, "package.json"),
		`{"name": "test"}`,
	)

	mock := &mockNodeTools{}
	envVars := []string{"FOO=bar"}
	execCtx := tools.ExecutionContext{
		BoundaryDir: root,
		EnvVars:     envVars,
	}
	scriptPath := filepath.Join(projectDir, "hook.js")

	projCtx, err := prepareNodeProject(
		t.Context(), mock, &mockCommandRunner{},
		scriptPath, execCtx,
	)

	require.NoError(t, err)
	require.NotNil(t, projCtx,
		"should find package.json alongside Python files")
	assert.Equal(t, projectDir, projCtx.ProjectDir)
	assert.True(t, mock.installCalled,
		"should install Node.js deps in mixed-language dir")
	assert.Equal(t, projectDir, mock.installDir)
}

// ---------------------------------------------------------------------------
// nodePackageManagerFromConfig tests
// ---------------------------------------------------------------------------

func TestNodePackageManagerFromConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  map[string]any
		wantPM  node.PackageManagerKind
		wantErr string
	}{
		{
			name:   "NilConfig",
			config: nil,
			wantPM: "",
		},
		{
			name:   "EmptyConfig",
			config: map[string]any{},
			wantPM: "",
		},
		{
			name: "EmptyString",
			config: map[string]any{
				"packageManager": "",
			},
			wantPM: "",
		},
		{
			name: "Npm",
			config: map[string]any{
				"packageManager": "npm",
			},
			wantPM: node.PackageManagerNpm,
		},
		{
			name: "Pnpm",
			config: map[string]any{
				"packageManager": "pnpm",
			},
			wantPM: node.PackageManagerPnpm,
		},
		{
			name: "Yarn",
			config: map[string]any{
				"packageManager": "yarn",
			},
			wantPM: node.PackageManagerYarn,
		},
		{
			name: "InvalidValue",
			config: map[string]any{
				"packageManager": "bun",
			},
			wantErr: "invalid packageManager config " +
				`value "bun"`,
		},
		{
			name: "WrongType",
			config: map[string]any{
				"packageManager": 123,
			},
			wantErr: "reading node hook config",
		},
		{
			name: "UnrelatedKeysIgnored",
			config: map[string]any{
				"other": "value",
			},
			wantPM: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm, err := nodePackageManagerFromConfig(
				tt.config,
			)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t,
					err.Error(), tt.wantErr,
				)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantPM, pm)
		})
	}
}

// ---------------------------------------------------------------------------
// prepareNodeProject with config override tests
// ---------------------------------------------------------------------------

func TestNodePrepare_PackageManagerOverride(
	t *testing.T,
) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	require.NoError(t, os.MkdirAll(projectDir, 0o700))
	writeFile(t,
		filepath.Join(projectDir, "package.json"),
		`{"name": "test"}`,
	)

	defaultMock := &mockNodeTools{}
	runner := &mockCommandRunner{
		runResult: exec.NewRunResult(
			0, "v20.0.0", "",
		),
	}

	execCtx := tools.ExecutionContext{
		BoundaryDir: root,
		Config: map[string]any{
			"packageManager": "pnpm",
		},
	}
	scriptPath := filepath.Join(projectDir, "hook.js")

	projCtx, err := prepareNodeProject(
		t.Context(), defaultMock, runner,
		scriptPath, execCtx,
	)

	require.NoError(t, err)
	require.NotNil(t, projCtx)
	assert.Equal(t, projectDir, projCtx.ProjectDir)

	// Default mock should not have been used — config
	// override replaced it with a real pnpm CLI.
	assert.False(t, defaultMock.installCalled,
		"default CLI should be replaced by config "+
			"override")

	// Last command should be pnpm install.
	assert.Equal(t, "pnpm", runner.lastRunArgs.Cmd,
		"should use pnpm for dependency installation")
}

func TestNodePrepare_InvalidPackageManager(t *testing.T) {
	mock := &mockNodeTools{}

	execCtx := tools.ExecutionContext{
		BoundaryDir: t.TempDir(),
		Config: map[string]any{
			"packageManager": "bun",
		},
	}

	projCtx, err := prepareNodeProject(
		t.Context(), mock, &mockCommandRunner{},
		"/any/hook.js", execCtx,
	)

	require.Error(t, err)
	assert.Nil(t, projCtx)
	assert.Contains(t, err.Error(),
		`invalid packageManager config value "bun"`)
	assert.False(t, mock.installCalled)
}

// ---------------------------------------------------------------------------
// buildNodeRunArgs tests
// ---------------------------------------------------------------------------

func TestBuildNodeRunArgs_StdOutWriter(t *testing.T) {
	var buf bytes.Buffer
	execCtx := tools.ExecutionContext{
		BoundaryDir: t.TempDir(),
		StdOut:      &buf,
	}

	scriptPath := filepath.Join(t.TempDir(), "hook.js")
	runArgs := buildNodeRunArgs(
		"node", nil, scriptPath, execCtx,
	)

	assert.NotNil(t, runArgs.StdOut,
		"StdOut should be set when execCtx.StdOut is non-nil")
	assert.Equal(t, &buf, runArgs.StdOut)
}

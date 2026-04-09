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
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
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
		t.Context(), mock, "/any/hook.js", execCtx,
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
		t.Context(), mock, scriptPath, execCtx,
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
		t.Context(), mock, scriptPath, execCtx,
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
		t.Context(), mock, scriptPath, execCtx,
	)

	require.Error(t, err)
	assert.Nil(t, projCtx)
	assert.Contains(t, err.Error(), "installing Node.js dependencies")
	assert.True(t, mock.installCalled)
}

func TestNodePrepare_PythonProjectIgnored(t *testing.T) {
	// When a requirements.txt is found instead of package.json,
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
		t.Context(), mock, scriptPath, execCtx,
	)

	require.NoError(t, err)
	assert.Nil(t, projCtx,
		"should not return a project context for non-JS projects")
	assert.False(t, mock.installCalled,
		"should not install deps for non-JS projects")
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

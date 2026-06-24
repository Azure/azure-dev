// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

func TestPromptDir_ReturnsAbsPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := newTestConsole()
	c.promptFsFn = func(opts input.ConsoleOptions, _ input.FsOptions) (string, error) {
		return dir, nil
	}
	got, err := promptDir(t.Context(), c, "pick dir")
	require.NoError(t, err)
	assert.NotEmpty(t, got)
}

func TestPromptDir_RetriesOnInvalidThenAccepts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	calls := 0
	c := newTestConsole()
	c.promptFsFn = func(opts input.ConsoleOptions, _ input.FsOptions) (string, error) {
		calls++
		if calls == 1 {
			return filepath_join(dir, "nope-does-not-exist"), nil
		}
		return dir, nil
	}
	got, err := promptDir(t.Context(), c, "pick dir")
	require.NoError(t, err)
	assert.NotEmpty(t, got)
	assert.Equal(t, 2, calls)
}

func TestPromptDir_PromptError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		return "", assertErr()
	}
	_, err := promptDir(t.Context(), c, "pick dir")
	require.Error(t, err)
}

func TestPromptDockerfile_DirectFilePath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dockerPath := filepath_join(dir, "Dockerfile")
	writeFile(t, dockerPath, "FROM scratch\n")
	c := newTestConsole()
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		return dockerPath, nil
	}
	got, err := promptDockerfile(t.Context(), c, "pick dockerfile")
	require.NoError(t, err)
	assert.NotEmpty(t, got)
}

func TestPromptDockerfile_DirWithDockerfile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath_join(dir, "Dockerfile"), "FROM scratch\n")
	c := newTestConsole()
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		return dir, nil
	}
	got, err := promptDockerfile(t.Context(), c, "pick dockerfile")
	require.NoError(t, err)
	assert.NotEmpty(t, got)
}

func TestPromptDockerfile_RetriesWhenMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath_join(dir, "Dockerfile"), "FROM scratch\n")
	calls := 0
	c := newTestConsole()
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		calls++
		if calls == 1 {
			return filepath_join(dir, "nothing-here"), nil
		}
		return dir, nil
	}
	got, err := promptDockerfile(t.Context(), c, "pick dockerfile")
	require.NoError(t, err)
	assert.NotEmpty(t, got)
	assert.Equal(t, 2, calls)
}

func TestPromptDockerfile_PromptError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		return "", assertErr()
	}
	_, err := promptDockerfile(t.Context(), c, "pick dockerfile")
	require.Error(t, err)
}

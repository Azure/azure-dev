// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package kustomize

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func Test_Edit(t *testing.T) {
	args := []string{"set", "image", "nginx=nginx:1.7.9"}

	t.Run("Success", func(t *testing.T) {
		ran := false
		var runArgs exec.RunArgs

		mockContext := mocks.NewMockContext(t.Context())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "kustomize edit")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				runArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.Edit(*mockContext.Context, args...)
		require.True(t, ran)
		require.NoError(t, err)

		expected := []string{"edit"}
		expected = append(expected, args...)

		require.Equal(t, "kustomize", runArgs.Cmd)
		require.Equal(t, "", runArgs.Cwd)
		require.Equal(t, expected, runArgs.Args)
	})

	t.Run("WithCwd", func(t *testing.T) {
		ran := false
		var runArgs exec.RunArgs

		mockContext := mocks.NewMockContext(t.Context())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "kustomize edit")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				runArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.
			WithCwd("/tmp").
			Edit(*mockContext.Context, args...)

		require.True(t, ran)
		require.NoError(t, err)

		expected := []string{"edit"}
		expected = append(expected, args...)

		require.Equal(t, "kustomize", runArgs.Cmd)
		require.Equal(t, "/tmp", runArgs.Cwd)
		require.Equal(t, expected, runArgs.Args)
	})

	t.Run("Failure", func(t *testing.T) {
		ran := false

		mockContext := mocks.NewMockContext(t.Context())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "kustomize edit")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				return exec.NewRunResult(1, "", ""), errors.New("failed to edit kustomize config")
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.Edit(*mockContext.Context, args...)

		require.True(t, ran)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to edit kustomize config")
	})
}

func TestCliName(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	cli := NewCli(mockContext.CommandRunner)
	assert.Equal(t, "kustomize", cli.Name())
}

func TestCliInstallUrl(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	cli := NewCli(mockContext.CommandRunner)
	assert.Equal(
		t,
		"https://aka.ms/azure-dev/kustomize-install",
		cli.InstallUrl(),
	)
}

func TestCheckInstalled(t *testing.T) {
	t.Run("ToolFoundAndVersionSucceeds", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.CommandRunner.MockToolInPath(
			"kustomize", nil,
		)
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(
					command, "kustomize version",
				)
			}).
			RespondFn(
				func(args exec.RunArgs) (exec.RunResult, error) {
					return exec.NewRunResult(
						0, "v5.3.0", "",
					), nil
				},
			)

		cli := NewCli(mockContext.CommandRunner)
		err := cli.CheckInstalled(*mockContext.Context)
		require.NoError(t, err)
	})

	t.Run("ToolFoundButVersionFails", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.CommandRunner.MockToolInPath(
			"kustomize", nil,
		)
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(
					command, "kustomize version",
				)
			}).
			RespondFn(
				func(args exec.RunArgs) (exec.RunResult, error) {
					return exec.NewRunResult(1, "", ""),
						errors.New("version failed")
				},
			)

		cli := NewCli(mockContext.CommandRunner)
		// CheckInstalled should still succeed even if
		// version fetch fails — it only logs the error.
		err := cli.CheckInstalled(*mockContext.Context)
		require.NoError(t, err)
	})

	t.Run("ToolNotInPath", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.CommandRunner.MockToolInPath(
			"kustomize",
			errors.New("kustomize not found in PATH"),
		)

		cli := NewCli(mockContext.CommandRunner)
		err := cli.CheckInstalled(*mockContext.Context)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestNewCli(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	cli := NewCli(mockContext.CommandRunner)
	require.NotNil(t, cli)
	// Verify the cli is usable after construction
	assert.Equal(t, "kustomize", cli.Name())
}

func TestWithCwd_Chaining(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	cli := NewCli(mockContext.CommandRunner)

	// WithCwd returns a shallow copy so the singleton Cli isn't mutated by
	// concurrent callers; the returned *Cli must be a different pointer with
	// the requested cwd, while the original remains untouched.
	result := cli.WithCwd("/some/path")
	assert.NotSame(t, cli, result)
	assert.Equal(t, "/some/path", result.cwd)
	assert.Equal(t, "", cli.cwd)

	// Returned *Cli must still be usable for subsequent chained calls.
	chained := result.WithCwd("/another/path")
	assert.NotSame(t, result, chained)
	assert.Equal(t, "/another/path", chained.cwd)
	assert.Equal(t, "/some/path", result.cwd)
}

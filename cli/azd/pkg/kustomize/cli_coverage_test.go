// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package kustomize

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCliName(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)
	assert.Equal(t, "kustomize", cli.Name())
}

func TestCliInstallUrl(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)
	assert.Equal(
		t,
		"https://aka.ms/azure-dev/kustomize-install",
		cli.InstallUrl(),
	)
}

func TestCheckInstalled(t *testing.T) {
	t.Run("ToolFoundAndVersionSucceeds", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
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
		mockContext := mocks.NewMockContext(context.Background())
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
		mockContext := mocks.NewMockContext(context.Background())
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
	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)
	require.NotNil(t, cli)
	// Verify the cli is usable after construction
	assert.Equal(t, "kustomize", cli.Name())
}

func TestWithCwd_Chaining(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)

	// WithCwd should return the same *Cli for chaining
	result := cli.WithCwd("/some/path")
	assert.Same(t, cli, result)
}

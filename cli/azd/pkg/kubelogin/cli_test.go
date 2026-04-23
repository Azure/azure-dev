// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package kubelogin

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

func TestNewCli(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)
	require.NotNil(t, cli)
}

func TestCliName(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)
	assert.Equal(t, "kubelogin", cli.Name())
}

func TestCliInstallUrl(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)
	assert.Equal(
		t,
		"https://aka.ms/azure-dev/kubelogin-install",
		cli.InstallUrl(),
	)
}

func TestCheckInstalled(t *testing.T) {
	t.Run("Found", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.MockToolInPath(
			"kubelogin", nil,
		)

		cli := NewCli(mockContext.CommandRunner)
		err := cli.CheckInstalled(*mockContext.Context)
		require.NoError(t, err)
	})

	t.Run("NotFound", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.MockToolInPath(
			"kubelogin",
			errors.New("kubelogin not found in PATH"),
		)

		cli := NewCli(mockContext.CommandRunner)
		err := cli.CheckInstalled(*mockContext.Context)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestConvertKubeConfig(t *testing.T) {
	t.Run("NilOptionsDefaultsToAzdLogin", func(t *testing.T) {
		var capturedArgs exec.RunArgs
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "kubelogin")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				capturedArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.ConvertKubeConfig(*mockContext.Context, nil)
		require.NoError(t, err)

		assert.Equal(t, "kubelogin", capturedArgs.Cmd)
		expected := []string{
			"convert-kubeconfig", "--login", "azd",
		}
		assert.Equal(t, expected, capturedArgs.Args)
	})

	t.Run("EmptyOptionsDefaultsToAzdLogin", func(t *testing.T) {
		var capturedArgs exec.RunArgs
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "kubelogin")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				capturedArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.ConvertKubeConfig(
			*mockContext.Context,
			&ConvertOptions{},
		)
		require.NoError(t, err)

		expected := []string{
			"convert-kubeconfig", "--login", "azd",
		}
		assert.Equal(t, expected, capturedArgs.Args)
	})

	t.Run("AllOptionsSet", func(t *testing.T) {
		var capturedArgs exec.RunArgs
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "kubelogin")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				capturedArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		opts := &ConvertOptions{
			Login:      "spn",
			TenantId:   "tenant-abc-123",
			Context:    "my-k8s-context",
			KubeConfig: "/home/user/.kube/config",
		}
		err := cli.ConvertKubeConfig(
			*mockContext.Context, opts,
		)
		require.NoError(t, err)

		assert.Equal(t, "kubelogin", capturedArgs.Cmd)
		expected := []string{
			"convert-kubeconfig",
			"--login", "spn",
			"--kubeconfig", "/home/user/.kube/config",
			"--tenant-id", "tenant-abc-123",
			"--context", "my-k8s-context",
		}
		assert.Equal(t, expected, capturedArgs.Args)
	})

	t.Run("OnlyKubeConfigSet", func(t *testing.T) {
		var capturedArgs exec.RunArgs
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "kubelogin")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				capturedArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		opts := &ConvertOptions{
			KubeConfig: "/tmp/kubeconfig",
		}
		err := cli.ConvertKubeConfig(
			*mockContext.Context, opts,
		)
		require.NoError(t, err)

		expected := []string{
			"convert-kubeconfig",
			"--login", "azd",
			"--kubeconfig", "/tmp/kubeconfig",
		}
		assert.Equal(t, expected, capturedArgs.Args)
	})

	t.Run("OnlyTenantIdSet", func(t *testing.T) {
		var capturedArgs exec.RunArgs
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "kubelogin")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				capturedArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		opts := &ConvertOptions{
			TenantId: "my-tenant",
		}
		err := cli.ConvertKubeConfig(
			*mockContext.Context, opts,
		)
		require.NoError(t, err)

		expected := []string{
			"convert-kubeconfig",
			"--login", "azd",
			"--tenant-id", "my-tenant",
		}
		assert.Equal(t, expected, capturedArgs.Args)
	})

	t.Run("OnlyContextSet", func(t *testing.T) {
		var capturedArgs exec.RunArgs
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "kubelogin")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				capturedArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		opts := &ConvertOptions{
			Context: "production",
		}
		err := cli.ConvertKubeConfig(
			*mockContext.Context, opts,
		)
		require.NoError(t, err)

		expected := []string{
			"convert-kubeconfig",
			"--login", "azd",
			"--context", "production",
		}
		assert.Equal(t, expected, capturedArgs.Args)
	})

	t.Run("CommandFailure", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "kubelogin")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.NewRunResult(1, "", "err"),
					errors.New("exit code 1")
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.ConvertKubeConfig(
			*mockContext.Context,
			&ConvertOptions{Login: "azd"},
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "converting kubeconfig")
	})

	t.Run("CustomLoginMethod", func(t *testing.T) {
		var capturedArgs exec.RunArgs
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "kubelogin")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				capturedArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		opts := &ConvertOptions{
			Login: "devicecode",
		}
		err := cli.ConvertKubeConfig(
			*mockContext.Context, opts,
		)
		require.NoError(t, err)

		expected := []string{
			"convert-kubeconfig",
			"--login", "devicecode",
		}
		assert.Equal(t, expected, capturedArgs.Args)
	})
}

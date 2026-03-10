// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package swa

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

var testPath = filepath.Join("projectPath", "service", "path")

func Test_SwaBuild(t *testing.T) {
	ran := false

	t.Run("NoErrors", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		swacli := NewCli(mockContext.CommandRunner)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "npx")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, testPath, args.Cwd)
			require.Equal(t, []string{
				"-y", swaCliPackage,
				"build", "-V",
			}, args.Args)

			return exec.RunResult{
				Stdout: "",
				Stderr: "",
				// if the returned `error` is nil we don't return an error. The underlying 'exec'
				// returns an error if the command returns a non-zero exit code so we don't actually
				// need to check it.
				ExitCode: 1,
			}, nil
		})

		err := swacli.Build(context.Background(), testPath, nil, nil)
		require.NoError(t, err)
		require.True(t, ran)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		swacli := NewCli(mockContext.CommandRunner)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "npx")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, testPath, args.Cwd)
			require.Equal(t, []string{
				"-y", swaCliPackage,
				"build", "-V",
			}, args.Args)

			return exec.RunResult{
				Stdout:   "stdout text",
				Stderr:   "stderr text",
				ExitCode: 1,
			}, errors.New("exit code: 1")
		})

		err := swacli.Build(context.Background(), testPath, nil, nil)
		require.True(t, ran)
		require.EqualError(
			t,
			err,
			"swa build: exit code: 1",
		)
	})
}

func Test_SwaBuild_PassesEnvVars(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	swacli := NewCli(mockContext.CommandRunner)

	var capturedArgs exec.RunArgs
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "npx")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		capturedArgs = args
		return exec.RunResult{}, nil
	})

	env := []string{"VITE_API_URL=https://api.example.com", "NODE_ENV=production"}
	err := swacli.Build(context.Background(), testPath, nil, env)
	require.NoError(t, err)
	require.Equal(t, env, capturedArgs.Env)
}

func Test_SwaDeploy(t *testing.T) {
	ran := false

	t.Run("NoErrors", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		swacli := NewCli(mockContext.CommandRunner)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "npx")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, testPath, args.Cwd)
			require.Equal(t, []string{
				"-y", swaCliPackage,
				"deploy",
				"--tenant-id", "tenantID",
				"--subscription-id", "subscriptionID",
				"--resource-group", "resourceGroupID",
				"--app-name", "appName",
				"--env", "default",
				"--no-use-keychain",
				"--deployment-token", "deploymentToken",
			}, args.Args)

			return exec.RunResult{
				Stdout: "",
				Stderr: "",
				// if the returned `error` is nil we don't return an error. The underlying 'exec'
				// returns an error if the command returns a non-zero exit code so we don't actually
				// need to check it.
				ExitCode: 1,
			}, nil
		})

		_, err := swacli.Deploy(
			context.Background(),
			testPath,
			"tenantID",
			"subscriptionID",
			"resourceGroupID",
			"appName",
			"default",
			"deploymentToken",
			DeployOptions{},
			nil,
		)
		require.NoError(t, err)
		require.True(t, ran)
	})

	t.Run("NoErrorsNoConfig", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		swacli := NewCli(mockContext.CommandRunner)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "npx")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, testPath, args.Cwd)
			require.Equal(t, []string{
				"-y", swaCliPackage,
				"deploy",
				"--tenant-id", "tenantID",
				"--subscription-id", "subscriptionID",
				"--resource-group", "resourceGroupID",
				"--app-name", "appName",
				"--env", "default",
				"--no-use-keychain",
				"--deployment-token", "deploymentToken",
				"--app-location", "appFolderPath",
				"--output-location", "outputRelativeFolderPath",
			}, args.Args)

			return exec.RunResult{
				Stdout: "",
				Stderr: "",
				// if the returned `error` is nil we don't return an error. The underlying 'exec'
				// returns an error if the command returns a non-zero exit code so we don't actually
				// need to check it.
				ExitCode: 1,
			}, nil
		})

		_, err := swacli.Deploy(
			context.Background(),
			testPath,
			"tenantID",
			"subscriptionID",
			"resourceGroupID",
			"appName",
			"default",
			"deploymentToken",
			DeployOptions{
				AppFolderPath:            "appFolderPath",
				OutputRelativeFolderPath: "outputRelativeFolderPath",
			},
			nil,
		)
		require.NoError(t, err)
		require.True(t, ran)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		swacli := NewCli(mockContext.CommandRunner)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "npx")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, testPath, args.Cwd)
			require.Equal(t, []string{
				"-y", swaCliPackage,
				"deploy",
				"--tenant-id", "tenantID",
				"--subscription-id", "subscriptionID",
				"--resource-group", "resourceGroupID",
				"--app-name", "appName",
				"--env", "default",
				"--no-use-keychain",
				"--deployment-token", "deploymentToken",
			}, args.Args)

			return exec.RunResult{
				Stdout:   "stdout text",
				Stderr:   "stderr text",
				ExitCode: 1,
			}, errors.New("exit code: 1")
		})

		_, err := swacli.Deploy(
			context.Background(),
			testPath,
			"tenantID",
			"subscriptionID",
			"resourceGroupID",
			"appName",
			"default",
			"deploymentToken",
			DeployOptions{},
			nil,
		)
		require.True(t, ran)
		require.EqualError(
			t,
			err,
			"swa deploy: exit code: 1",
		)
	})
}

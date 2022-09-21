// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azcli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_GetFunctionAppProperties(t *testing.T) {
	t.Run("NoErrors", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(context.Background())
		azCli := GetAzCli(*mockContext.Context)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "az functionapp show")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, []string{
				"functionapp", "show",
				"--subscription", "subID",
				"--resource-group", "resourceGroupID",
				"--name", "funcName",
				"--output", "json",
			}, args.Args)

			require.True(t, args.EnrichError, "errors are enriched")

			return exec.RunResult{
				Stdout: `{"hostNames":["https://test.com"]}`,
				Stderr: "stderr text",
				// if the returned `error` is nil we don't return an error. The underlying 'exec'
				// returns an error if the command returns a non-zero exit code so we don't actually
				// need to check it.
				ExitCode: 1,
			}, nil
		})

		props, err := azCli.GetFunctionAppProperties(context.Background(), "subID", "resourceGroupID", "funcName")
		require.NoError(t, err)
		require.Equal(t, []string{"https://test.com"}, props.HostNames)
		require.True(t, ran)
	})

	t.Run("Error", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(context.Background())
		azCli := GetAzCli(*mockContext.Context)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "az functionapp show")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, []string{
				"functionapp", "show",
				"--subscription", "subID",
				"--resource-group", "resourceGroupID",
				"--name", "funcName",
				"--output", "json",
			}, args.Args)

			require.True(t, args.EnrichError, "errors are enriched")
			return exec.RunResult{
				Stdout:   "",
				Stderr:   "stderr text",
				ExitCode: 1,
			}, errors.New("example error message")
		})

		props, err := azCli.GetFunctionAppProperties(context.Background(), "subID", "resourceGroupID", "funcName")
		require.Equal(t, AzCliFunctionAppProperties{}, props)
		require.True(t, ran)
		require.EqualError(t, err, "failed getting functionapp properties: example error message")
	})
}

func Test_DeployFunctionAppUsingZipFile(t *testing.T) {
	t.Run("NoErrors", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(context.Background())
		azCli := GetAzCli(*mockContext.Context)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "az functionapp deployment source config-zip")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, []string{
				"functionapp", "deployment", "source", "config-zip",
				"--subscription", "subID",
				"--resource-group", "resourceGroupID",
				"--name", "funcName",
				"--src", "test.zip",
				"--build-remote", "true",
				"--timeout", "3600",
			}, args.Args)

			require.True(t, args.EnrichError, "errors are enriched")

			return exec.RunResult{
				Stdout: "stdout text",
				Stderr: "stderr text",
				// if the returned `error` is nil we don't return an error. The underlying 'exec'
				// returns an error if the command returns a non-zero exit code so we don't actually
				// need to check it.
				ExitCode: 1,
			}, nil
		})

		res, err := azCli.DeployFunctionAppUsingZipFile(context.Background(), "subID", "resourceGroupID", "funcName", "test.zip")
		require.NoError(t, err)
		require.True(t, ran)
		require.Equal(t, "stdout text", res)
	})

	t.Run("Error", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(context.Background())
		azCli := GetAzCli(*mockContext.Context)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "az functionapp deployment source config-zip")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, []string{
				"functionapp", "deployment", "source", "config-zip",
				"--subscription", "subID",
				"--resource-group", "resourceGroupID",
				"--name", "funcName",
				"--src", "test.zip",
				"--build-remote", "true",
				"--timeout", "3600",
			}, args.Args)

			require.True(t, args.EnrichError, "errors are enriched")
			return exec.RunResult{
				Stdout:   "stdout text",
				Stderr:   "stderr text",
				ExitCode: 1,
			}, errors.New("this error is printed verbatim but would be enriched since we passed args.EnrichError.true")
		})

		_, err := azCli.DeployFunctionAppUsingZipFile(context.Background(), "subID", "resourceGroupID", "funcName", "test.zip")
		require.True(t, ran)
		require.EqualError(t, err, "failed deploying function app: this error is printed verbatim but would be enriched since we passed args.EnrichError.true")
	})
}

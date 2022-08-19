// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azcli

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/stretchr/testify/require"
)

func Test_GetFunctionAppProperties(t *testing.T) {
	tempAZCLI := NewAzCli(NewAzCliArgs{
		EnableDebug:     false,
		EnableTelemetry: true,
	})
	azcli := tempAZCLI.(*azCli)

	ran := false

	t.Run("NoErrors", func(t *testing.T) {
		azcli.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			ran = true

			require.Equal(t, []string{
				"functionapp", "show",
				"--subscription", "subID",
				"--resource-group", "resourceGroupID",
				"--name", "funcName",
				"--output", "json",
			}, args.Args)

			require.True(t, args.EnrichError, "errors are enriched")

			return executil.RunResult{
				Stdout: `{"hostNames":["https://test.com"]}`,
				Stderr: "stderr text",
				// if the returned `error` is nil we don't return an error. The underlying 'exec'
				// returns an error if the command returns a non-zero exit code so we don't actually
				// need to check it.
				ExitCode: 1,
			}, nil
		}

		props, err := azcli.GetFunctionAppProperties(context.Background(), "subID", "resourceGroupID", "funcName")
		require.NoError(t, err)
		require.Equal(t, []string{"https://test.com"}, props.HostNames)
		require.True(t, ran)
	})

	t.Run("Error", func(t *testing.T) {
		azcli.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			ran = true

			require.Equal(t, []string{
				"functionapp", "show",
				"--subscription", "subID",
				"--resource-group", "resourceGroupID",
				"--name", "funcName",
				"--output", "json",
			}, args.Args)

			require.True(t, args.EnrichError, "errors are enriched")
			return executil.RunResult{
				Stdout:   "",
				Stderr:   "stderr text",
				ExitCode: 1,
			}, errors.New("example error message")
		}

		props, err := azcli.GetFunctionAppProperties(context.Background(), "subID", "resourceGroupID", "funcName")
		require.Equal(t, AzCliFunctionAppProperties{}, props)
		require.True(t, ran)
		require.EqualError(t, err, "failed getting functionapp properties: example error message")
	})
}

func Test_DeployFunctionAppUsingZipFile(t *testing.T) {
	tempAZCLI := NewAzCli(NewAzCliArgs{
		EnableDebug:     false,
		EnableTelemetry: true,
	})
	azcli := tempAZCLI.(*azCli)

	ran := false

	t.Run("NoErrors", func(t *testing.T) {
		azcli.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
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

			return executil.RunResult{
				Stdout: "stdout text",
				Stderr: "stderr text",
				// if the returned `error` is nil we don't return an error. The underlying 'exec'
				// returns an error if the command returns a non-zero exit code so we don't actually
				// need to check it.
				ExitCode: 1,
			}, nil
		}

		res, err := azcli.DeployFunctionAppUsingZipFile(context.Background(), "subID", "resourceGroupID", "funcName", "test.zip")
		require.NoError(t, err)
		require.True(t, ran)
		require.Equal(t, "stdout text", res)
	})

	t.Run("Error", func(t *testing.T) {
		azcli.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
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
			return executil.RunResult{
				Stdout:   "stdout text",
				Stderr:   "stderr text",
				ExitCode: 1,
			}, errors.New("this error is printed verbatim but would be enriched since we passed args.EnrichError.true")
		}

		_, err := azcli.DeployFunctionAppUsingZipFile(context.Background(), "subID", "resourceGroupID", "funcName", "test.zip")
		require.True(t, ran)
		require.EqualError(t, err, "failed deploying function app: this error is printed verbatim but would be enriched since we passed args.EnrichError.true")
	})
}

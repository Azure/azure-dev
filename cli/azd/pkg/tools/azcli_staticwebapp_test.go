// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/stretchr/testify/require"
)

func Test_GetStaticWebAppProperties(t *testing.T) {
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
				"staticwebapp", "show",
				"--subscription", "subID",
				"--resource-group", "resourceGroupID",
				"--name", "appName",
				"--output", "json",
			}, args.Args)

			require.True(t, args.EnrichError, "errors are enriched")

			return executil.RunResult{
				Stdout: `{"defaultHostname":"https://test.com"}`,
				Stderr: "stderr text",
				// if the returned `error` is nil we don't return an error. The underlying 'exec'
				// returns an error if the command returns a non-zero exit code so we don't actually
				// need to check it.
				ExitCode: 1,
			}, nil
		}

		props, err := azcli.GetStaticWebAppProperties(context.Background(), "subID", "resourceGroupID", "appName")
		require.NoError(t, err)
		require.Equal(t, "https://test.com", props.DefaultHostname)
		require.True(t, ran)
	})

	t.Run("Error", func(t *testing.T) {
		azcli.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			ran = true

			require.Equal(t, []string{
				"staticwebapp", "show",
				"--subscription", "subID",
				"--resource-group", "resourceGroupID",
				"--name", "appName",
				"--output", "json",
			}, args.Args)

			require.True(t, args.EnrichError, "errors are enriched")
			return executil.RunResult{
				Stdout:   "",
				Stderr:   "stderr text",
				ExitCode: 1,
			}, errors.New("example error message")
		}

		props, err := azcli.GetStaticWebAppProperties(context.Background(), "subID", "resourceGroupID", "appName")
		require.Equal(t, AzCliStaticWebAppProperties{}, props)
		require.True(t, ran)
		require.EqualError(t, err, "failed getting staticwebapp properties: example error message")
	})
}

func Test_GetStaticWebAppEnvironmentProperties(t *testing.T) {
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
				"staticwebapp", "environment", "show",
				"--subscription", "subID",
				"--resource-group", "resourceGroupID",
				"--name", "appName",
				"--environment", "default",
				"--output", "json",
			}, args.Args)

			require.True(t, args.EnrichError, "errors are enriched")

			return executil.RunResult{
				Stdout: `{"hostname":"default-environment-name.azurestaticapps.net"}`,
				Stderr: "stderr text",
				// if the returned `error` is nil we don't return an error. The underlying 'exec'
				// returns an error if the command returns a non-zero exit code so we don't actually
				// need to check it.
				ExitCode: 1,
			}, nil
		}

		props, err := azcli.GetStaticWebAppEnvironmentProperties(context.Background(), "subID", "resourceGroupID", "appName", "default")
		require.NoError(t, err)
		require.Equal(t, "default-environment-name.azurestaticapps.net", props.Hostname)
		require.True(t, ran)
	})

	t.Run("Error", func(t *testing.T) {
		azcli.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			ran = true

			require.Equal(t, []string{
				"staticwebapp", "environment", "show",
				"--subscription", "subID",
				"--resource-group", "resourceGroupID",
				"--name", "appName",
				"--environment", "default",
				"--output", "json",
			}, args.Args)

			require.True(t, args.EnrichError, "errors are enriched")
			return executil.RunResult{
				Stdout:   "",
				Stderr:   "stderr text",
				ExitCode: 1,
			}, errors.New("example error message")
		}

		props, err := azcli.GetStaticWebAppEnvironmentProperties(context.Background(), "subID", "resourceGroupID", "appName", "default")
		require.Equal(t, AzCliStaticWebAppEnvironmentProperties{}, props)
		require.True(t, ran)
		require.EqualError(t, err, "failed getting staticwebapp environment properties: example error message")
	})
}

func Test_GetStaticWebAppApiKey(t *testing.T) {
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
				"staticwebapp", "secrets", "list",
				"--subscription", "subID",
				"--resource-group", "resourceGroupID",
				"--name", "appName",
				"--query", "properties.apiKey",
				"--output", "tsv",
			}, args.Args)

			require.True(t, args.EnrichError, "errors are enriched")

			return executil.RunResult{
				Stdout: "ABC123",
				Stderr: "stderr text",
				// if the returned `error` is nil we don't return an error. The underlying 'exec'
				// returns an error if the command returns a non-zero exit code so we don't actually
				// need to check it.
				ExitCode: 1,
			}, nil
		}

		apiKey, err := azcli.GetStaticWebAppApiKey(context.Background(), "subID", "resourceGroupID", "appName")
		require.NoError(t, err)
		require.Equal(t, "ABC123", apiKey)
		require.True(t, ran)
	})

	t.Run("Error", func(t *testing.T) {
		azcli.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			ran = true

			require.Equal(t, []string{
				"staticwebapp", "secrets", "list",
				"--subscription", "subID",
				"--resource-group", "resourceGroupID",
				"--name", "appName",
				"--query", "properties.apiKey",
				"--output", "tsv",
			}, args.Args)

			require.True(t, args.EnrichError, "errors are enriched")
			return executil.RunResult{
				Stdout:   "",
				Stderr:   "stderr text",
				ExitCode: 1,
			}, errors.New("example error message")
		}

		apiKey, err := azcli.GetStaticWebAppApiKey(context.Background(), "subID", "resourceGroupID", "appName")
		require.Equal(t, "", apiKey)
		require.True(t, ran)
		require.EqualError(t, err, "failed getting staticwebapp api key: example error message")
	})
}

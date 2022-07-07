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

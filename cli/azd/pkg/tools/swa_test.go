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

func Test_SwaLogin(t *testing.T) {
	tempSwaCli := NewSwaCli()
	swacli := tempSwaCli.(*swaCli)

	ran := false

	t.Run("NoErrors", func(t *testing.T) {
		swacli.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			ran = true

			require.Equal(t, []string{
				"-y", "@azure/static-web-apps-cli",
				"login",
				"--tenant-id", "tenantID",
				"--subscription-id", "subscriptionID",
				"--resource-group", "resourceGroupID",
				"--app-name", "appName",
			}, args.Args)

			return executil.RunResult{
				Stdout: "",
				Stderr: "",
				// if the returned `error` is nil we don't return an error. The underlying 'exec'
				// returns an error if the command returns a non-zero exit code so we don't actually
				// need to check it.
				ExitCode: 1,
			}, nil
		}

		err := swacli.Login(context.Background(), "tenantID", "subscriptionID", "resourceGroupID", "appName")
		require.NoError(t, err)
		require.True(t, ran)
	})

	t.Run("Error", func(t *testing.T) {
		swacli.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			ran = true

			require.Equal(t, []string{
				"-y", "@azure/static-web-apps-cli",
				"login",
				"--tenant-id", "tenantID",
				"--subscription-id", "subscriptionID",
				"--resource-group", "resourceGroupID",
				"--app-name", "appName",
			}, args.Args)

			return executil.RunResult{
				Stdout:   "stdout text",
				Stderr:   "stderr text",
				ExitCode: 1,
			}, errors.New("example error message")
		}

		err := swacli.Login(context.Background(), "tenantID", "subscriptionID", "resourceGroupID", "appName")
		require.True(t, ran)
		require.EqualError(t, err, "swa login: exit code: 1, stdout: stdout text, stderr: stderr text: example error message")
	})
}

func Test_SwaBuild(t *testing.T) {
	tempSwaCli := NewSwaCli()
	swacli := tempSwaCli.(*swaCli)

	ran := false

	t.Run("NoErrors", func(t *testing.T) {
		swacli.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			ran = true

			require.Equal(t, "./appFolderPath", args.Cwd)
			require.Equal(t, []string{
				"-y", "@azure/static-web-apps-cli",
				"build",
				"--app-location", ".",
				"--output-location", "build",
			}, args.Args)

			return executil.RunResult{
				Stdout: "",
				Stderr: "",
				// if the returned `error` is nil we don't return an error. The underlying 'exec'
				// returns an error if the command returns a non-zero exit code so we don't actually
				// need to check it.
				ExitCode: 1,
			}, nil
		}

		err := swacli.Build(context.Background(), "./appFolderPath", "build")
		require.NoError(t, err)
		require.True(t, ran)
	})

	t.Run("Error", func(t *testing.T) {
		swacli.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			ran = true

			require.Equal(t, "./appFolderPath", args.Cwd)
			require.Equal(t, []string{
				"-y", "@azure/static-web-apps-cli",
				"build",
				"--app-location", ".",
				"--output-location", "build",
			}, args.Args)

			return executil.RunResult{
				Stdout:   "stdout text",
				Stderr:   "stderr text",
				ExitCode: 1,
			}, errors.New("example error message")
		}

		err := swacli.Build(context.Background(), "./appFolderPath", "build")
		require.True(t, ran)
		require.EqualError(t, err, "swa build: exit code: 1, stdout: stdout text, stderr: stderr text: example error message")
	})
}

func Test_SwaDeploy(t *testing.T) {
	tempSwaCli := NewSwaCli()
	swacli := tempSwaCli.(*swaCli)

	ran := false

	t.Run("NoErrors", func(t *testing.T) {
		swacli.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			ran = true

			require.Equal(t, "./appFolderPath", args.Cwd)
			require.Equal(t, []string{
				"-y", "@azure/static-web-apps-cli",
				"deploy",
				"--tenant-id", "tenantID",
				"--subscription-id", "subscriptionID",
				"--resource-group", "resourceGroupID",
				"--app-name", "appName",
				"--app-location", ".",
				"--output-location", "build",
				"--env", "default",
				"--deployment-token", "deploymentToken",
			}, args.Args)

			return executil.RunResult{
				Stdout: "",
				Stderr: "",
				// if the returned `error` is nil we don't return an error. The underlying 'exec'
				// returns an error if the command returns a non-zero exit code so we don't actually
				// need to check it.
				ExitCode: 1,
			}, nil
		}

		_, err := swacli.Deploy(context.Background(), "tenantID", "subscriptionID", "resourceGroupID", "appName", "./appFolderPath", "build", "default", "deploymentToken")
		require.NoError(t, err)
		require.True(t, ran)
	})

	t.Run("Error", func(t *testing.T) {
		swacli.runWithResultFn = func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			ran = true

			require.Equal(t, "./appFolderPath", args.Cwd)
			require.Equal(t, []string{
				"-y", "@azure/static-web-apps-cli",
				"deploy",
				"--tenant-id", "tenantID",
				"--subscription-id", "subscriptionID",
				"--resource-group", "resourceGroupID",
				"--app-name", "appName",
				"--app-location", ".",
				"--output-location", "build",
				"--env", "default",
				"--deployment-token", "deploymentToken",
			}, args.Args)

			return executil.RunResult{
				Stdout:   "stdout text",
				Stderr:   "stderr text",
				ExitCode: 1,
			}, errors.New("example error message")
		}

		_, err := swacli.Deploy(context.Background(), "tenantID", "subscriptionID", "resourceGroupID", "appName", "./appFolderPath", "build", "default", "deploymentToken")
		require.True(t, ran)
		require.EqualError(t, err, "swa deploy: exit code: 1, stdout: stdout text, stderr: stderr text: example error message")
	})
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package swa

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_SwaBuild(t *testing.T) {
	ran := false

	t.Run("NoErrors", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		swacli := NewSwaCli(mockContext.CommandRunner)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "npx")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, "./projectPath", args.Cwd)
			require.Equal(t, []string{
				"-y", "@azure/static-web-apps-cli@1.0.0",
				"build",
				"--app-location", "service/path",
				"--output-location", "build",
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

		err := swacli.Build(context.Background(), "./projectPath", "service/path", "build")
		require.NoError(t, err)
		require.True(t, ran)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		swacli := NewSwaCli(mockContext.CommandRunner)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "npx")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, "./projectPath", args.Cwd)
			require.Equal(t, []string{
				"-y", "@azure/static-web-apps-cli@1.0.0",
				"build",
				"--app-location", "service/path",
				"--output-location", "build",
			}, args.Args)

			return exec.RunResult{
				Stdout:   "stdout text",
				Stderr:   "stderr text",
				ExitCode: 1,
			}, errors.New("example error message")
		})

		err := swacli.Build(context.Background(), "./projectPath", "service/path", "build")
		require.True(t, ran)
		require.EqualError(
			t,
			err,
			"swa build: exit code: 1, stdout: stdout text, stderr: stderr text: example error message",
		)
	})
}

func Test_SwaDeploy(t *testing.T) {
	ran := false

	t.Run("NoErrors", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		swacli := NewSwaCli(mockContext.CommandRunner)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "npx")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, "./projectPath", args.Cwd)
			require.Equal(t, []string{
				"-y", "@azure/static-web-apps-cli@1.0.0",
				"deploy",
				"--tenant-id", "tenantID",
				"--subscription-id", "subscriptionID",
				"--resource-group", "resourceGroupID",
				"--app-name", "appName",
				"--app-location", "service/path",
				"--output-location", "build",
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
			"./projectPath",
			"tenantID",
			"subscriptionID",
			"resourceGroupID",
			"appName",
			"service/path",
			"build",
			"default",
			"deploymentToken",
		)
		require.NoError(t, err)
		require.True(t, ran)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		swacli := NewSwaCli(mockContext.CommandRunner)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "npx")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ran = true

			require.Equal(t, "./projectPath", args.Cwd)
			require.Equal(t, []string{
				"-y", "@azure/static-web-apps-cli@1.0.0",
				"deploy",
				"--tenant-id", "tenantID",
				"--subscription-id", "subscriptionID",
				"--resource-group", "resourceGroupID",
				"--app-name", "appName",
				"--app-location", "service/path",
				"--output-location", "build",
				"--env", "default",
				"--no-use-keychain",
				"--deployment-token", "deploymentToken",
			}, args.Args)

			return exec.RunResult{
				Stdout:   "stdout text",
				Stderr:   "stderr text",
				ExitCode: 1,
			}, errors.New("example error message")
		})

		_, err := swacli.Deploy(
			context.Background(),
			"./projectPath",
			"tenantID",
			"subscriptionID",
			"resourceGroupID",
			"appName",
			"service/path",
			"build",
			"default",
			"deploymentToken",
		)
		require.True(t, ran)
		require.EqualError(
			t,
			err,
			"swa deploy: exit code: 1, stdout: stdout text, stderr: stderr text: example error message",
		)
	})
}

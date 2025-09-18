// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package github_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	mockexec "github.com/azure/azure-dev/cli/azd/test/mocks/exec"
	"github.com/stretchr/testify/require"
)

func TestGithubCLI(t *testing.T) {
	t.Run("mock", func(t *testing.T) {
		commandRunner := mockexec.NewMockCommandRunner()
		ctx := exec.WithCommandRunner(context.Background(), commandRunner)

		add := func(verbAndURL string) *mockexec.CommandExpression {
			called := false

			return commandRunner.When(func(args exec.RunArgs, command string) bool {
				defer func() { called = true }()

				// ex: 'api', 'X', verb, URL
				return !called && verbAndURL == args.Args[2]+" "+args.Args[3]
			})
		}

		doThisTest(ctx, t, "richardpark-msft/copilot-auth-tests", "copilot2", add)
	})

	// TODO: how do we handle live testing resources, like a GitHub repo?
	// t.Run("live", func(t *testing.T) {
	// 	commandRunner := exec.NewCommandRunner(os.Stdin, os.Stdout, os.Stderr)
	// 	ctx := exec.WithCommandRunner(context.Background(), commandRunner)

	// 	doThisTest(ctx, t, "richardpark-msft/copilot-auth-tests", "copilot2", func(verbAndURL string) *mockexec.CommandExpression {
	// 		// (unused, but needed to compile)
	// 		return &mockexec.CommandExpression{}
	// 	})
	// })
}

func doThisTest(ctx context.Context, t *testing.T, repoName string, envName string, add func(verbAndURL string) *mockexec.CommandExpression) {
	cli := github.NewGitHubCli(ctx)

	add("PUT /repos/richardpark-msft/copilot-auth-tests/environments/copilot2").Respond(exec.NewRunResult(0, "", ""))
	err := cli.CreateEnvironmentIfNotExist(ctx, repoName, envName)
	require.NoError(t, err)

	t.Cleanup(func() {
		add("DELETE /repos/richardpark-msft/copilot-auth-tests/environments/copilot2").Respond(exec.NewRunResult(0, "", ""))
		err = cli.DeleteEnvironment(ctx, repoName, envName)
		require.NoError(t, err)

		add("GET /repos/richardpark-msft/copilot-auth-tests/environments/copilot2/variables/hello").Respond(exec.NewRunResult(0, "", ""))
		_, err = cli.GetEnvironmentVariable(ctx, repoName, envName, "hello")
		require.Error(t, err)
	})

	{
		add("POST /repos/richardpark-msft/copilot-auth-tests/environments/copilot2/variables").Respond(exec.NewRunResult(0, "", ""))
		add("PATCH /repos/richardpark-msft/copilot-auth-tests/environments/copilot2/variables/hello").SetError(errors.New("this fails"))

		created, err := cli.CreateOrUpdateEnvironmentVariable(ctx, repoName, envName, "hello", "world")
		require.NoError(t, err)
		require.True(t, created, "brand new variable, so created is true")

		contents, err := os.ReadFile("testdata/getenv.json")
		require.NoError(t, err)

		add("GET /repos/richardpark-msft/copilot-auth-tests/environments/copilot2/variables/hello").Respond(exec.NewRunResult(0, string(contents), ""))
		value, err := cli.GetEnvironmentVariable(ctx, repoName, envName, "hello")
		require.NoError(t, err)
		require.Equal(t, "world", value)
	}

	// updating an existing variable
	{
		// this time the PATCH fails, so we'll fall back to creating it instead.
		add("PATCH /repos/richardpark-msft/copilot-auth-tests/environments/copilot2/variables/hello").Respond(exec.NewRunResult(0, "", ""))

		created, err := cli.CreateOrUpdateEnvironmentVariable(ctx, repoName, envName, "hello", "world2")
		require.NoError(t, err)
		require.False(t, created, "variable existed already, created is false")

		contents, err := os.ReadFile("testdata/getenv2.json")
		require.NoError(t, err)

		add("GET /repos/richardpark-msft/copilot-auth-tests/environments/copilot2/variables/hello").Respond(exec.NewRunResult(0, string(contents), ""))
		value, err := cli.GetEnvironmentVariable(ctx, repoName, envName, "hello")
		require.NoError(t, err)
		require.Equal(t, "world2", value)
	}
}

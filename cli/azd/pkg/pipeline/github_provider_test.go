// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_gitHub_provider_getRepoDetails(t *testing.T) {
	t.Run("https", func(t *testing.T) {
		provider := &GitHubScmProvider{}
		ctx := context.Background()
		details, e := provider.gitRepoDetails(ctx, "https://github.com/Azure/azure-dev.git")
		require.NoError(t, e)
		require.Equal(t, "Azure", details.owner)
		require.Equal(t, "azure-dev", details.repoName)
	})
	t.Run("ssh", func(t *testing.T) {
		provider := &GitHubScmProvider{}
		ctx := context.Background()
		details, e := provider.gitRepoDetails(ctx, "git@github.com:Azure/azure-dev.git")
		require.NoError(t, e)
		require.EqualValues(t, "Azure", details.owner)
		require.EqualValues(t, "azure-dev", details.repoName)
	})
	t.Run("error", func(t *testing.T) {
		provider := &GitHubScmProvider{}
		ctx := context.Background()
		details, e := provider.gitRepoDetails(ctx, "git@other.com:Azure/azure-dev.git")
		require.Error(t, e, ErrRemoteHostIsNotGitHub)
		require.EqualValues(t, (*gitRepositoryDetails)(nil), details)
	})
}

func Test_gitHub_provider_preConfigure_check(t *testing.T) {
	t.Run("success with all default values", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		setupGithubCliMocks(mockContext)

		provider := createGitHubCiProvider(t, mockContext)
		updatedConfig, err := provider.preConfigureCheck(
			*mockContext.Context,
			PipelineManagerArgs{},
			provisioning.Options{},
			"",
		)
		require.NoError(t, err)
		require.False(t, updatedConfig)

		// No warnings on console
		consoleLog := mockContext.Console.Output()
		require.Len(t, consoleLog, 0)
	})

	t.Run("fails with terraform & federated", func(t *testing.T) {
		pipelineManagerArgs := PipelineManagerArgs{
			PipelineAuthTypeName: string(AuthTypeFederated),
		}

		infraOptions := provisioning.Options{
			Provider: provisioning.Terraform,
		}

		mockContext := mocks.NewMockContext(context.Background())
		setupGithubCliMocks(mockContext)

		provider := createGitHubCiProvider(t, mockContext)
		updatedConfig, err := provider.preConfigureCheck(*mockContext.Context, pipelineManagerArgs, infraOptions, "")
		require.Error(t, err)
		require.False(t, updatedConfig)
		require.True(t, errors.Is(err, ErrAuthNotSupported))
	})

	t.Run("warning with terraform & default value", func(t *testing.T) {
		pipelineManagerArgs := PipelineManagerArgs{
			PipelineAuthTypeName: "",
		}

		infraOptions := provisioning.Options{
			Provider: provisioning.Terraform,
		}

		mockContext := mocks.NewMockContext(context.Background())
		setupGithubCliMocks(mockContext)

		provider := createGitHubCiProvider(t, mockContext)
		updatedConfig, err := provider.preConfigureCheck(
			*mockContext.Context, pipelineManagerArgs, infraOptions, "")
		require.NoError(t, err)
		require.False(t, updatedConfig)

		consoleLog := mockContext.Console.Output()
		require.Len(t, consoleLog, 1)
		require.Contains(t, consoleLog[0], "Warning: Terraform provisioning does not support federated authentication")
	})
}

func createGitHubCiProvider(t *testing.T, mockContext *mocks.MockContext) CiProvider {
	env := environment.New("test")
	ghCli, err := github.NewGitHubCli(
		*mockContext.Context,
		mockContext.Console,
		mockContext.CommandRunner,
	)
	require.NoError(t, err)

	return NewGitHubCiProvider(
		env,
		mockContext.SubscriptionCredentialProvider,
		entraid.NewEntraIdService(
			mockContext.SubscriptionCredentialProvider,
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
		),
		ghCli,
		git.NewGitCli(mockContext.CommandRunner),
		mockContext.Console,
		mockContext.HttpClient,
	)
}

func setupGithubCliMocks(mockContext *mocks.MockContext) {
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "auth status")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "--version")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, fmt.Sprintf("gh version %s", github.GitHubCliVersion), ""), nil
	})
}

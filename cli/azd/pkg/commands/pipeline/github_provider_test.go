// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
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
		setupGithubAuthMock(mockContext)

		provider := NewGitHubCiProvider(mockContext.Credentials, mockContext.CommandRunner)
		err := provider.preConfigureCheck(
			*mockContext.Context,
			mockContext.Console,
			PipelineManagerArgs{},
			provisioning.Options{},
		)
		require.NoError(t, err)

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
		setupGithubAuthMock(mockContext)

		provider := NewGitHubCiProvider(mockContext.Credentials, mockContext.CommandRunner)
		err := provider.preConfigureCheck(*mockContext.Context, mockContext.Console, pipelineManagerArgs, infraOptions)
		require.Error(t, err)
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
		setupGithubAuthMock(mockContext)

		provider := NewGitHubCiProvider(mockContext.Credentials, mockContext.CommandRunner)
		err := provider.preConfigureCheck(*mockContext.Context, mockContext.Console, pipelineManagerArgs, infraOptions)
		require.NoError(t, err)

		consoleLog := mockContext.Console.Output()
		require.Len(t, consoleLog, 1)
		require.Contains(t, consoleLog[0], "WARNING: Terraform provisioning does not support federated authentication")
	})
}

func setupGithubAuthMock(mockContext *mocks.MockContext) {
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "gh auth status")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})
}

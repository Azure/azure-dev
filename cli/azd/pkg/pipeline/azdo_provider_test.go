// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdo"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_azdo_provider_getRepoDetails(t *testing.T) {
	t.Run("error", func(t *testing.T) {
		// arrange
		provider := getAzdoScmProviderTestHarness(mockinput.NewMockConsole())
		testOrgName := provider.env.Dotenv()[azdo.AzDoEnvironmentOrgName]
		testRepoName := provider.env.Dotenv()[azdo.AzDoEnvironmentRepoName]
		ctx := context.Background()

		// act
		details, e := provider.gitRepoDetails(ctx, "https://fake_org@dev.azure.com/fake_org/repo1/_git/repo1")

		// assert
		require.NoError(t, e)
		require.EqualValues(t, testOrgName, details.owner)
		require.EqualValues(t, testRepoName, details.repoName)
		require.EqualValues(t, false, details.pushStatus)
	})

	t.Run("ssh not supported", func(t *testing.T) {
		// arrange
		provider := getAzdoScmProviderTestHarness(mockinput.NewMockConsole())
		ctx := context.Background()

		// act
		details, e := provider.gitRepoDetails(ctx, "git@ssh.dev.azure.com:v3/fake_org/repo1/repo1")

		// assert
		require.Error(t, e, ErrSSHNotSupported)
		require.EqualValues(t, (*gitRepositoryDetails)(nil), details)
	})

	t.Run("non azure devops https remote", func(t *testing.T) {
		//arrange
		provider := &AzdoScmProvider{}
		ctx := context.Background()

		//act
		details, e := provider.gitRepoDetails(ctx, "https://github.com/Azure/azure-dev.git")

		//asserts
		require.Error(t, e, ErrRemoteHostIsNotAzDo)
		require.EqualValues(t, (*gitRepositoryDetails)(nil), details)
	})

	t.Run("non azure devops git remote", func(t *testing.T) {
		//arrange
		provider := &AzdoScmProvider{}
		ctx := context.Background()

		//act
		details, e := provider.gitRepoDetails(ctx, "git@github.com:Azure/azure-dev.git")

		//asserts
		require.Error(t, e, ErrRemoteHostIsNotAzDo)
		require.EqualValues(t, (*gitRepositoryDetails)(nil), details)
	})
}

func Test_azdo_scm_provider_preConfigureCheck(t *testing.T) {
	t.Run("accepts a PAT via system environment variables", func(t *testing.T) {
		// arrange
		testPat := "12345"
		envManager := &mockenv.MockEnvManager{}
		provider := getEmptyAzdoScmProviderTestHarness(envManager, mockinput.NewMockConsole())
		t.Setenv(azdo.AzDoEnvironmentOrgName, "testOrg")
		t.Setenv(azdo.AzDoPatName, testPat)
		ctx := context.Background()

		// act
		updatedConfig, e := provider.preConfigureCheck(ctx, PipelineManagerArgs{}, provisioning.Options{}, "")

		// assert
		require.NoError(t, e)
		require.False(t, updatedConfig)
	})

	t.Run("returns an error if no pat is provided", func(t *testing.T) {
		// arrange
		ostest.Unsetenv(t, azdo.AzDoPatName)
		ostest.Setenv(t, azdo.AzDoEnvironmentOrgName, "testOrg")
		testConsole := mockinput.NewMockConsole()
		testPat := "testPAT12345"
		testConsole.WhenPrompt(func(options input.ConsoleOptions) bool {
			return options.Message == "Personal Access Token (PAT):"
		}).Respond(testPat)
		ctx := context.Background()
		envManager := &mockenv.MockEnvManager{}
		provider := getEmptyAzdoScmProviderTestHarness(envManager, testConsole)

		// act
		updatedConfig, e := provider.preConfigureCheck(ctx, PipelineManagerArgs{}, provisioning.Options{}, "")

		// assert
		require.Nil(t, e)
		// PAT is not persisted to .env
		require.EqualValues(t, "", provider.env.Dotenv()[azdo.AzDoPatName])
		require.True(t, updatedConfig)
	})
}

func Test_azdo_ci_provider_preConfigureCheck(t *testing.T) {
	t.Run("success with default options", func(t *testing.T) {
		ctx := context.Background()

		testConsole := mockinput.NewMockConsole()
		testPat := "testPAT12345"
		testConsole.WhenPrompt(func(options input.ConsoleOptions) bool {
			return options.Message == "Personal Access Token (PAT):"
		}).Respond(testPat)
		provider := getAzdoCiProviderTestHarness(testConsole)
		pipelineManagerArgs := PipelineManagerArgs{
			PipelineAuthTypeName: "",
		}

		updatedConfig, err := provider.preConfigureCheck(ctx, pipelineManagerArgs, provisioning.Options{}, "")
		require.NoError(t, err)
		require.True(t, updatedConfig)
	})

	t.Run("fails if auth type is set to federated", func(t *testing.T) {
		ctx := context.Background()

		testConsole := mockinput.NewMockConsole()
		pipelineManagerArgs := PipelineManagerArgs{
			PipelineAuthTypeName: string(AuthTypeFederated),
		}
		provider := getAzdoCiProviderTestHarness(testConsole)

		updatedConfig, err := provider.preConfigureCheck(ctx, pipelineManagerArgs, provisioning.Options{}, "")
		require.Error(t, err)
		require.False(t, updatedConfig)
		require.True(t, errors.Is(err, ErrAuthNotSupported))
	})
}

func Test_saveEnvironmentConfig(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	env := environment.New("test")

	t.Run("saves to environment file", func(t *testing.T) {
		// arrange
		key := "test"
		value := "12345"
		envManager := &mockenv.MockEnvManager{}
		envManager.On("Save", mock.Anything, env).Return(nil)

		provider := getEmptyAzdoScmProviderTestHarness(envManager, mockinput.NewMockConsole())
		provider.env = env
		// act
		e := provider.saveEnvironmentConfig(*mockContext.Context, key, value)
		// assert
		readValue := env.Dotenv()[key]
		require.EqualValues(t, readValue, value)
		require.NoError(t, e)

		envManager.AssertCalled(t, "Save", mock.Anything, env)
	})

}

func getEmptyAzdoScmProviderTestHarness(envManager environment.Manager, console input.Console) *AzdoScmProvider {
	return &AzdoScmProvider{
		envManager: envManager,
		env:        environment.New("test"),
		console:    console,
	}
}

func getAzdoScmProviderTestHarness(console input.Console) *AzdoScmProvider {
	return &AzdoScmProvider{
		env: environment.NewWithValues(
			"test-env",
			map[string]string{
				azdo.AzDoEnvironmentOrgName:       "fake_org",
				azdo.AzDoEnvironmentProjectName:   "project1",
				azdo.AzDoEnvironmentProjectIdName: "12345",
				azdo.AzDoEnvironmentRepoName:      "repo1",
				azdo.AzDoEnvironmentRepoIdName:    "9876",
				azdo.AzDoEnvironmentRepoWebUrl:    "https://repo",
			},
		),
		console: console,
	}
}

func getAzdoCiProviderTestHarness(console input.Console) *AzdoCiProvider {
	return &AzdoCiProvider{
		Env: environment.NewWithValues(
			"test-env",
			map[string]string{
				azdo.AzDoEnvironmentOrgName:       "fake_org",
				azdo.AzDoEnvironmentProjectName:   "project1",
				azdo.AzDoEnvironmentProjectIdName: "12345",
				azdo.AzDoEnvironmentRepoName:      "repo1",
				azdo.AzDoEnvironmentRepoIdName:    "9876",
				azdo.AzDoEnvironmentRepoWebUrl:    "https://repo",
			},
		),
		console: console,
	}
}

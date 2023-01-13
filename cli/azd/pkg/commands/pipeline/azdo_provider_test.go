// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdo"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/require"
)

func Test_azdo_provider_getRepoDetails(t *testing.T) {
	t.Run("error", func(t *testing.T) {
		// arrange
		provider := getAzdoScmProviderTestHarness()
		testOrgName := provider.Env.Values[azdo.AzDoEnvironmentOrgName]
		testRepoName := provider.Env.Values[azdo.AzDoEnvironmentRepoName]
		ctx := context.Background()

		// act
		details, e := provider.gitRepoDetails(ctx, "https://fake_org@dev.azure.com/fake_org/repo1/_git/repo1")

		// assert
		require.NoError(t, e)
		require.EqualValues(t, testOrgName, details.owner)
		require.EqualValues(t, testRepoName, details.repoName)
		require.EqualValues(t, false, details.pushStatus)
	})

	t.Run("ssh", func(t *testing.T) {
		// arrange
		provider := getAzdoScmProviderTestHarness()
		testOrgName := provider.Env.Values[azdo.AzDoEnvironmentOrgName]
		testRepoName := provider.Env.Values[azdo.AzDoEnvironmentRepoName]
		ctx := context.Background()

		// act
		details, e := provider.gitRepoDetails(ctx, "git@ssh.dev.azure.com:v3/fake_org/repo1/repo1")

		// assert
		require.NoError(t, e)
		require.EqualValues(t, testOrgName, details.owner)
		require.EqualValues(t, testRepoName, details.repoName)
		require.EqualValues(t, false, details.pushStatus)
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
		provider := getEmptyAzdoScmProviderTestHarness()
		t.Setenv(azdo.AzDoEnvironmentOrgName, "testOrg")
		t.Setenv(azdo.AzDoPatName, testPat)
		testConsole := mockinput.NewMockConsole()
		ctx := context.Background()

		// act
		e := provider.preConfigureCheck(ctx, testConsole, PipelineManagerArgs{}, provisioning.Options{})

		// assert
		require.NoError(t, e)
	})

	t.Run("returns an error if no pat is provided", func(t *testing.T) {
		// arrange
		ostest.Unsetenv(t, azdo.AzDoPatName)
		ostest.Setenv(t, azdo.AzDoEnvironmentOrgName, "testOrg")
		provider := getEmptyAzdoScmProviderTestHarness()
		testConsole := mockinput.NewMockConsole()
		testPat := "testPAT12345"
		testConsole.WhenPrompt(func(options input.ConsoleOptions) bool {
			return options.Message == "Personal Access Token (PAT):"
		}).Respond(testPat)
		ctx := context.Background()

		// act
		e := provider.preConfigureCheck(ctx, testConsole, PipelineManagerArgs{}, provisioning.Options{})

		// assert
		require.Nil(t, e)
		// PAT is not persisted to .env
		require.EqualValues(t, "", provider.Env.Values[azdo.AzDoPatName])
	})
}

func Test_azdo_ci_provider_preConfigureCheck(t *testing.T) {
	t.Run("success with default options", func(t *testing.T) {
		ctx := context.Background()
		provider := getAzdoCiProviderTestHarness()
		testConsole := mockinput.NewMockConsole()
		testPat := "testPAT12345"
		testConsole.WhenPrompt(func(options input.ConsoleOptions) bool {
			return options.Message == "Personal Access Token (PAT):"
		}).Respond(testPat)

		pipelineManagerArgs := PipelineManagerArgs{
			PipelineAuthTypeName: "",
		}

		err := provider.preConfigureCheck(ctx, testConsole, pipelineManagerArgs, provisioning.Options{})
		require.NoError(t, err)
	})

	t.Run("fails if auth type is set to federated", func(t *testing.T) {
		ctx := context.Background()
		provider := getAzdoCiProviderTestHarness()
		testConsole := mockinput.NewMockConsole()

		pipelineManagerArgs := PipelineManagerArgs{
			PipelineAuthTypeName: string(AuthTypeFederated),
		}

		err := provider.preConfigureCheck(ctx, testConsole, pipelineManagerArgs, provisioning.Options{})
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrAuthNotSupported))
	})
}

func Test_saveEnvironmentConfig(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("saves to environment file", func(t *testing.T) {
		// arrange
		key := "test"
		value := "12345"
		provider := getEmptyAzdoScmProviderTestHarness()
		envPath := filepath.Join(tempDir, "test")
		provider.Env = environment.EmptyWithRoot(envPath)
		// act
		e := provider.saveEnvironmentConfig(key, value)
		// assert
		writtenEnv, err := environment.FromRoot(envPath)
		require.NoError(t, err)

		require.EqualValues(t, writtenEnv.Values[key], value)
		require.NoError(t, e)
	})

}

func getEmptyAzdoScmProviderTestHarness() *AzdoScmProvider {
	return &AzdoScmProvider{
		Env: &environment.Environment{
			Values: map[string]string{},
		},
	}
}

func getAzdoScmProviderTestHarness() *AzdoScmProvider {
	return &AzdoScmProvider{
		Env: &environment.Environment{
			Values: map[string]string{
				azdo.AzDoEnvironmentOrgName:       "fake_org",
				azdo.AzDoEnvironmentProjectName:   "project1",
				azdo.AzDoEnvironmentProjectIdName: "12345",
				azdo.AzDoEnvironmentRepoName:      "repo1",
				azdo.AzDoEnvironmentRepoIdName:    "9876",
				azdo.AzDoEnvironmentRepoWebUrl:    "https://repo",
			},
		},
	}
}

func getAzdoCiProviderTestHarness() *AzdoCiProvider {
	return &AzdoCiProvider{
		Env: &environment.Environment{
			Values: map[string]string{
				azdo.AzDoEnvironmentOrgName:       "fake_org",
				azdo.AzDoEnvironmentProjectName:   "project1",
				azdo.AzDoEnvironmentProjectIdName: "12345",
				azdo.AzDoEnvironmentRepoName:      "repo1",
				azdo.AzDoEnvironmentRepoIdName:    "9876",
				azdo.AzDoEnvironmentRepoWebUrl:    "https://repo",
			},
		},
	}
}

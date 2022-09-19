// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"os"
	"path"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdo"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
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
		provider := &AzdoHubScmProvider{}
		ctx := context.Background()

		//act
		details, e := provider.gitRepoDetails(ctx, "https://github.com/Azure/azure-dev.git")

		//asserts
		require.Error(t, e, ErrRemoteHostIsNotAzDo)
		require.EqualValues(t, (*gitRepositoryDetails)(nil), details)
	})

	t.Run("non azure devops git remote", func(t *testing.T) {
		//arrange
		provider := &AzdoHubScmProvider{}
		ctx := context.Background()

		//act
		details, e := provider.gitRepoDetails(ctx, "git@github.com:Azure/azure-dev.git")

		//asserts
		require.Error(t, e, ErrRemoteHostIsNotAzDo)
		require.EqualValues(t, (*gitRepositoryDetails)(nil), details)
	})
}

func Test_azdo_provider_preConfigureCheck(t *testing.T) {
	t.Run("accepts a PAT via system environment variables", func(t *testing.T) {
		// arrange
		testPat := "12345"
		provider := getEmptyAzdoScmProviderTestHarness()
		os.Setenv(azdo.AzDoEnvironmentOrgName, "testOrg")
		os.Setenv(azdo.AzDoPatName, testPat)
		testConsole := &circularConsole{}
		ctx := context.Background()

		// act
		e := provider.preConfigureCheck(ctx, testConsole)

		// assert
		require.NoError(t, e)

		//cleanup
		os.Unsetenv(azdo.AzDoPatName)
	})

	t.Run("returns an error if no pat is provided", func(t *testing.T) {
		// arrange
		os.Unsetenv(azdo.AzDoPatName)
		os.Setenv(azdo.AzDoEnvironmentOrgName, "testOrg")
		provider := getEmptyAzdoScmProviderTestHarness()
		testConsole := &circularConsole{}
		ctx := context.Background()

		// act
		e := provider.preConfigureCheck(ctx, testConsole)

		// assert
		require.Error(t, e)
	})

}

func Test_saveEnvironmentConfig(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("saves to environment file", func(t *testing.T) {
		// arrange
		key := "test"
		value := "12345"
		provider := getEmptyAzdoScmProviderTestHarness()
		envPath := path.Join(tempDir, ".test.env")
		provider.Env = environment.EmptyWithFile(envPath)
		// act
		e := provider.saveEnvironmentConfig(key, value)
		// assert
		writtenEnv, err := environment.FromFile(envPath)
		require.NoError(t, err)

		require.EqualValues(t, writtenEnv.Values[key], value)
		require.NoError(t, e)
	})

}
func getEmptyAzdoScmProviderTestHarness() *AzdoHubScmProvider {
	return &AzdoHubScmProvider{
		Env: &environment.Environment{
			Values: map[string]string{},
		},
	}
}

func getAzdoScmProviderTestHarness() *AzdoHubScmProvider {
	return &AzdoHubScmProvider{
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

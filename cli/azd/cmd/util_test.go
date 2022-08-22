package cmd

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_promptEnvironmentName(t *testing.T) {
	t.Run("valid name", func(t *testing.T) {
		mockConsole := mocks.NewMockConsole()
		mockConsole.WhenPrompt(func(options input.ConsoleOptions) bool {
			return true
		}).SetError(errors.New("prompt should not be called for valid environment name"))

		environmentName := "hello"

		err := ensureValidEnvironmentName(context.Background(), &environmentName, mockConsole)

		require.NoError(t, err)
	})

	t.Run("empty name gets prompted", func(t *testing.T) {
		environmentName := ""

		mockConsole := mocks.NewMockConsole()
		mockConsole.WhenPrompt(func(options input.ConsoleOptions) bool {
			return true
		}).Respond("someEnv")

		err := ensureValidEnvironmentName(context.Background(), &environmentName, mockConsole)

		require.NoError(t, err)
		require.Equal(t, "someEnv", environmentName)
	})

	t.Run("duplicate resource groups ignored", func(t *testing.T) {
		cli := &fakeAZCLI{
			GetSubscriptionDeploymentResult: struct {
				Dep azcli.AzCliDeployment
				Err error
			}{
				Dep: azcli.AzCliDeployment{
					Properties: azcli.AzCliDeploymentProperties{
						Dependencies: []azcli.AzCliDeploymentPropertiesDependency{
							{
								DependsOn: []azcli.AzCliDeploymentPropertiesBasicDependency{
									{
										ResourceName: "groupA",
										ResourceType: string(infra.AzureResourceTypeResourceGroup),
									},
									{
										ResourceName: "groupB",
										ResourceType: string(infra.AzureResourceTypeResourceGroup),
									},
									{
										ResourceName: "ignoredForWrongType",
										ResourceType: string(infra.AzureResourceTypeStorageAccount),
									},
								},
							},
							{
								DependsOn: []azcli.AzCliDeploymentPropertiesBasicDependency{
									{
										ResourceName: "groupA",
										ResourceType: string(infra.AzureResourceTypeResourceGroup),
									},
									{
										ResourceName: "groupB",
										ResourceType: string(infra.AzureResourceTypeResourceGroup),
									},
									{
										ResourceName: "groupC",
										ResourceType: string(infra.AzureResourceTypeResourceGroup),
									},
								},
							},
						},
					},
				},
			},
		}

		groups, err := azureutil.GetResourceGroupsForDeployment(context.Background(), cli, "sub-id", "deployment-name")
		require.NoError(t, err)

		sort.Strings(groups)
		require.Equal(t, []string{"groupA", "groupB", "groupC"}, groups)
	})
}

type fakeAZCLI struct {
	azcli.AzCli

	GetSubscriptionDeploymentResult struct {
		Dep azcli.AzCliDeployment
		Err error
	}
}

func (cli *fakeAZCLI) GetSubscriptionDeployment(_ context.Context, subscriptionId string, deploymentName string) (azcli.AzCliDeployment, error) {
	return cli.GetSubscriptionDeploymentResult.Dep, cli.GetSubscriptionDeploymentResult.Err
}

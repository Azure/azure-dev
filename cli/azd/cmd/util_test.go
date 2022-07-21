package cmd

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/AlecAivazis/survey/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/stretchr/testify/require"
)

func Test_promptEnvironmentName(t *testing.T) {
	t.Run("valid name", func(t *testing.T) {
		environmentName := "hello"

		err := ensureValidEnvironmentName(&environmentName, func(p survey.Prompt, response interface{}) error {
			return errors.New("prompt should not be called for valid environment name")
		})

		require.NoError(t, err)
	})

	t.Run("empty name gets prompted", func(t *testing.T) {
		environmentName := ""

		err := ensureValidEnvironmentName(&environmentName, func(p survey.Prompt, response interface{}) error {
			ptr, ok := response.(*string)
			require.True(t, ok)

			*ptr = "someEnv"
			return nil
		})

		require.NoError(t, err)
		require.Equal(t, "someEnv", environmentName)
	})

	t.Run("duplicate resource groups ignored", func(t *testing.T) {
		cli := &fakeAZCLI{
			GetSubscriptionDeploymentResult: struct {
				Dep tools.AzCliDeployment
				Err error
			}{
				Dep: tools.AzCliDeployment{
					Properties: tools.AzCliDeploymentProperties{
						Dependencies: []tools.AzCliDeploymentPropertiesDependency{
							{
								DependsOn: []tools.AzCliDeploymentPropertiesBasicDependency{
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
								DependsOn: []tools.AzCliDeploymentPropertiesBasicDependency{
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

		resourceManager := infra.NewAzureResourceManager(cli)
		groups, err := resourceManager.GetResourceGroupsForDeployment(context.Background(), "sub-id", "deployment-name")
		require.NoError(t, err)

		sort.Strings(groups)
		require.Equal(t, []string{"groupA", "groupB", "groupC"}, groups)
	})
}

type fakeAZCLI struct {
	tools.AzCli

	GetSubscriptionDeploymentResult struct {
		Dep tools.AzCliDeployment
		Err error
	}
}

func (cli *fakeAZCLI) GetSubscriptionDeployment(_ context.Context, subscriptionId string, deploymentName string) (tools.AzCliDeployment, error) {
	return cli.GetSubscriptionDeploymentResult.Dep, cli.GetSubscriptionDeploymentResult.Err
}

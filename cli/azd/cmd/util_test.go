package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_promptEnvironmentName(t *testing.T) {
	t.Run("valid name", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
			return true
		}).SetError(errors.New("prompt should not be called for valid environment name"))

		environmentName := "hello"

		err := ensureValidEnvironmentName(*mockContext.Context, &environmentName, mockContext.Console)

		require.NoError(t, err)
	})

	t.Run("empty name gets prompted", func(t *testing.T) {
		environmentName := ""

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
			return true
		}).Respond("someEnv")

		err := ensureValidEnvironmentName(*mockContext.Context, &environmentName, mockContext.Console)

		require.NoError(t, err)
		require.Equal(t, "someEnv", environmentName)
	})

	t.Run("duplicate resource groups ignored", func(t *testing.T) {
		mockDeployment := azcli.AzCliDeployment{
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
		}

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "az deployment sub show")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			jsonBytes, _ := json.Marshal(mockDeployment)

			return exec.NewRunResult(0, string(jsonBytes), ""), nil
		})

		resourceManager := infra.NewAzureResourceManager(*mockContext.Context)
		groups, err := resourceManager.GetResourceGroupsForDeployment(*mockContext.Context, "sub-id", "deployment-name")
		require.NoError(t, err)

		sort.Strings(groups)
		require.Equal(t, []string{"groupA", "groupB", "groupC"}, groups)
	})
}

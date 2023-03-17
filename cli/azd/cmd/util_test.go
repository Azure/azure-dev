package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
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
		mockDeployment := armresources.DeploymentExtended{
			Properties: &armresources.DeploymentPropertiesExtended{
				Dependencies: []*armresources.Dependency{
					{
						DependsOn: []*armresources.BasicDependency{
							{
								ResourceName: convert.RefOf("groupA"),
								ResourceType: convert.RefOf(string(infra.AzureResourceTypeResourceGroup)),
							},
							{
								ResourceName: convert.RefOf("groupB"),
								ResourceType: convert.RefOf(string(infra.AzureResourceTypeResourceGroup)),
							},
							{
								ResourceName: convert.RefOf("ignoredForWrongType"),
								ResourceType: convert.RefOf(string(infra.AzureResourceTypeStorageAccount)),
							},
						},
					},
					{
						DependsOn: []*armresources.BasicDependency{
							{
								ResourceName: convert.RefOf("groupA"),
								ResourceType: convert.RefOf(string(infra.AzureResourceTypeResourceGroup)),
							},
							{
								ResourceName: convert.RefOf("groupB"),
								ResourceType: convert.RefOf(string(infra.AzureResourceTypeResourceGroup)),
							},
							{
								ResourceName: convert.RefOf("groupC"),
								ResourceType: convert.RefOf(string(infra.AzureResourceTypeResourceGroup)),
							},
						},
					},
				},
			},
		}

		mockContext := mocks.NewMockContext(context.Background())

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet && strings.Contains(
				request.URL.Path,
				"/subscriptions/sub-id/providers/Microsoft.Resources/deployments",
			)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			subscriptionsListBytes, _ := json.Marshal(mockDeployment)

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBuffer(subscriptionsListBytes)),
			}, nil
		})

		azCli := mockazcli.NewAzCliFromMockContext(mockContext)

		resourceManager := infra.NewAzureResourceManager(azCli)
		groups, err := resourceManager.GetResourceGroupsForDeployment(*mockContext.Context, "sub-id", "deployment-name")
		require.NoError(t, err)

		sort.Strings(groups)
		require.Equal(t, []string{"groupA", "groupB", "groupC"}, groups)
	})
}

func Test_createAndInitEnvironment(t *testing.T) {
	t.Run("invalid name", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		tempDir := t.TempDir()
		azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
		invalidEnvName := "*!33"
		_, err := createAndInitEnvironment(
			*mockContext.Context,
			environmentSpec{
				environmentName: invalidEnvName,
			},
			azdContext,
			mockContext.Console,
		)
		require.ErrorContains(
			t,
			err,
			fmt.Sprintf("environment name '%s' is invalid (it should contain only alphanumeric characters and hyphens)\n",
				invalidEnvName))
	})

	t.Run("env already exists", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		tempDir := t.TempDir()
		validName := "azdEnv"
		err := os.MkdirAll(filepath.Join(tempDir, ".azure", validName), 0755)
		require.NoError(t, err)
		azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)

		_, err = createAndInitEnvironment(
			*mockContext.Context,
			environmentSpec{
				environmentName: validName,
			},
			azdContext,
			mockContext.Console,
		)
		require.ErrorContains(
			t,
			err,
			fmt.Sprintf("environment '%s' already exists",
				validName))
	})
}

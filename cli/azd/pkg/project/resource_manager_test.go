package project

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/stretchr/testify/require"
)

type testInitFunc func(*mocks.MockContext)

// Validates that the resource group is correctly resolved from different configuration
// 1. Resource group referenced in service config
// 2. Resource group referenced in project
// 3. Resource group referenced in environment variable
// 4. Resource group tagged with azd-env-name
func Test_ResourceManager_GetTargetResource(t *testing.T) {
	taggedResourceGroup := &armresources.ResourceGroup{
		ID: to.Ptr(fmt.Sprintf(
			"/subscriptions/%s/resourceGroups/%s",
			"SUBSCRIPTION_id",
			"TAGGED_RESOURCE_GROUP",
		)),
		Name:     to.Ptr("TAGGED_RESOURCE_GROUP"),
		Type:     to.Ptr("Microsoft.Resources/resourceGroups"),
		Location: to.Ptr("eastus2"),
	}

	fromProjectConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageJavaScript)
	fromProjectConfig.Project.ResourceGroupName = osutil.NewExpandableString("PROJECT_RESOURCE_GROUP")

	fromServiceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageJavaScript)
	fromServiceConfig.Project.ResourceGroupName = osutil.NewExpandableString("PROJECT_RESOURCE_GROUP")
	fromServiceConfig.ResourceGroupName = osutil.NewExpandableString("SERVICE_RESOURCE_GROUP")

	tests := []struct {
		name                  string
		env                   *environment.Environment
		serviceConfig         *ServiceConfig
		expectedResourceGroup string
		init                  testInitFunc
	}{
		{
			name: "ResourceGroupFromTag",
			init: func(mockContext *mocks.MockContext) {
				setupGetResourceGroupMock(mockContext, taggedResourceGroup)
			},
			env: environment.NewWithValues("test", map[string]string{
				environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
			}),
			serviceConfig:         createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageJavaScript),
			expectedResourceGroup: "TAGGED_RESOURCE_GROUP",
		},
		{
			name: "ResourceGroupFromEnvVar",
			env: environment.NewWithValues("test", map[string]string{
				environment.ResourceGroupEnvVarName:  "ENV_VAR_RESOURCE_GROUP",
				environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
			}),
			serviceConfig:         createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageJavaScript),
			expectedResourceGroup: "ENV_VAR_RESOURCE_GROUP",
		},
		{
			name: "ResourceGroupFromProject",
			env: environment.NewWithValues("test", map[string]string{
				environment.ResourceGroupEnvVarName:  "ENV_VAR_RESOURCE_GROUP",
				environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
			}),
			serviceConfig:         fromProjectConfig,
			expectedResourceGroup: "PROJECT_RESOURCE_GROUP",
		},
		{
			name: "ResourceGroupFromService",
			env: environment.NewWithValues("test", map[string]string{
				environment.ResourceGroupEnvVarName:  "ENV_VAR_RESOURCE_GROUP",
				environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
			}),
			serviceConfig:         fromServiceConfig,
			expectedResourceGroup: "SERVICE_RESOURCE_GROUP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
			resourceService := azapi.NewResourceService(
				mockContext.SubscriptionCredentialProvider,
				mockContext.ArmClientOptions,
			)
			deploymentService := mockazapi.NewStandardDeploymentsFromMockContext(mockContext)

			if tt.init != nil {
				tt.init(mockContext)
			}

			expectedResource := &armresources.GenericResourceExpanded{
				ID:       to.Ptr("RESOURCE_ID"),
				Name:     to.Ptr("RESOURCE_NAME"),
				Type:     to.Ptr("Microsoft.Web/sites"),
				Location: to.Ptr("eastus2"),
			}

			setupGetResourceMock(mockContext, expectedResource)

			azureResourceManager := infra.NewAzureResourceManager(resourceService, deploymentService)
			resourceManager := NewResourceManager(tt.env, deploymentService, resourceService, azureResourceManager)
			targetResource, err := resourceManager.GetTargetResource(
				*mockContext.Context,
				tt.env.GetSubscriptionId(),
				tt.serviceConfig,
			)

			require.NoError(t, err)
			require.NotNil(t, targetResource)
			require.Equal(t, tt.expectedResourceGroup, targetResource.ResourceGroupName())
			require.Equal(t, "RESOURCE_NAME", targetResource.ResourceName())
			require.Equal(t, tt.env.GetSubscriptionId(), targetResource.SubscriptionId())
		})
	}
}

func setupGetResourceGroupMock(mockContext *mocks.MockContext, resourceGroup *armresources.ResourceGroup) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasSuffix(request.URL.Path, "/resourcegroups") && strings.Contains(request.URL.RawQuery, "filter=")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		result := armresources.ResourceGroupListResult{
			Value: []*armresources.ResourceGroup{
				resourceGroup,
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, result)
	})
}

func setupGetResourceMock(mockContext *mocks.MockContext, resource *armresources.GenericResourceExpanded) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasSuffix(request.URL.Path, "/resources") && strings.Contains(request.URL.RawQuery, "filter=")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		result := armresources.ResourceListResult{
			Value: []*armresources.GenericResourceExpanded{
				resource,
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, result)
	})
}

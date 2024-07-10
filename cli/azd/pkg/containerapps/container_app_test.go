package containerapps

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazsdk"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/require"
)

func Test_ContainerApp_GetIngressConfiguration(t *testing.T) {
	subscriptionId := "SUBSCRIPTION_ID"
	location := "eastus2"
	resourceGroup := "RESOURCE_GROUP"
	appName := "APP_NAME"
	hostName := fmt.Sprintf("%s.%s.azurecontainerapps.io", appName, location)

	containerApp := &armappcontainers.ContainerApp{
		Location: &location,
		Name:     &appName,
		Properties: &armappcontainers.ContainerAppProperties{
			Configuration: &armappcontainers.Configuration{
				ActiveRevisionsMode: convert.RefOf(armappcontainers.ActiveRevisionsModeSingle),
				Ingress: &armappcontainers.Ingress{
					Fqdn: &hostName,
				},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockRequest := mockazsdk.MockContainerAppGet(mockContext, subscriptionId, resourceGroup, appName, containerApp)

	cas := NewContainerAppService(
		mockContext.SubscriptionCredentialProvider,
		mockContext.HttpClient,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)
	ingressConfig, err := cas.GetIngressConfiguration(*mockContext.Context, subscriptionId, resourceGroup, appName)
	require.NoError(t, err)
	require.NotNil(t, ingressConfig)

	expectedPath := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s",
		subscriptionId,
		resourceGroup,
		appName,
	)

	require.Equal(t, expectedPath, mockRequest.URL.Path)
	require.Equal(t, hostName, ingressConfig.HostNames[0])
}

func Test_ContainerApp_AddRevision(t *testing.T) {
	subscriptionId := "SUBSCRIPTION_ID"
	location := "eastus2"
	resourceGroup := "RESOURCE_GROUP"
	appName := "APP_NAME"
	originalImageName := "ORIGINAL_IMAGE_NAME"
	updatedImageName := "UPDATED_IMAGE_NAME"
	originalRevisionName := "ORIGINAL_REVISION_NAME"
	updatedRevisionName := "UPDATED_REVISION_NAME"

	containerApp := &armappcontainers.ContainerApp{
		Location: &location,
		Name:     &appName,
		Properties: &armappcontainers.ContainerAppProperties{
			LatestRevisionName: &originalRevisionName,
			Configuration: &armappcontainers.Configuration{
				ActiveRevisionsMode: convert.RefOf(armappcontainers.ActiveRevisionsModeSingle),
				Secrets: []*armappcontainers.Secret{
					{
						Name:  convert.RefOf("secret"),
						Value: nil,
					},
				},
			},
			Template: &armappcontainers.Template{
				Containers: []*armappcontainers.Container{
					{
						Image: &originalImageName,
					},
				},
			},
		},
	}

	revision := &armappcontainers.Revision{
		Properties: &armappcontainers.RevisionProperties{
			Template: &armappcontainers.Template{
				Containers: []*armappcontainers.Container{
					{
						Image: &updatedRevisionName,
					},
				},
			},
		},
	}

	secrets := &armappcontainers.SecretsCollection{
		Value: []*armappcontainers.ContainerAppSecret{
			{
				Name:  convert.RefOf("secret"),
				Value: convert.RefOf("value"),
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	_ = mockazsdk.MockContainerAppGet(mockContext, subscriptionId, resourceGroup, appName, containerApp)
	getRevisionRequest := mockazsdk.MockContainerAppRevisionGet(
		mockContext,
		subscriptionId,
		resourceGroup,
		appName,
		originalRevisionName,
		revision,
	)
	_ = mockazsdk.MockContainerAppSecretsList(mockContext, subscriptionId, resourceGroup, appName, secrets)
	updateContainerAppRequest := mockazsdk.MockContainerAppUpdate(
		mockContext,
		subscriptionId,
		resourceGroup,
		appName,
		containerApp,
	)

	cas := NewContainerAppService(
		mockContext.SubscriptionCredentialProvider,
		mockContext.HttpClient,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)
	err := cas.AddRevision(*mockContext.Context, subscriptionId, resourceGroup, appName, updatedImageName)
	require.NoError(t, err)

	// Verify lastest revision is read
	expectedGetRevisionPath := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s/revisions/%s",
		subscriptionId,
		resourceGroup,
		appName,
		originalRevisionName,
	)

	require.Equal(t, expectedGetRevisionPath, getRevisionRequest.URL.Path)

	// Verify container image is updated
	var updatedContainerApp *armappcontainers.ContainerApp
	jsonDecoder := json.NewDecoder(updateContainerAppRequest.Body)
	err = jsonDecoder.Decode(&updatedContainerApp)
	require.NoError(t, err)
	require.Equal(t, updatedImageName, *updatedContainerApp.Properties.Template.Containers[0].Image)
	require.Equal(t, "azd-0", *updatedContainerApp.Properties.Template.RevisionSuffix)
}

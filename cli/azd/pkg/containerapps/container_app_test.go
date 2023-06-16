package containerapps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v2"
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

	cas := NewContainerAppService(mockContext.SubscriptionCredentialProvider, mockContext.HttpClient, clock.NewMock())
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

func Test_ContainerApp_ValidateRevision(t *testing.T) {
	subscriptionId := "SUBSCRIPTION_ID"
	resourceGroup := "RESOURCE_GROUP"
	appName := "APP_NAME"
	revisionName := "NEW_REVISION"
	expectedRevision := &armappcontainers.Revision{
		Name: &revisionName,
		Properties: &armappcontainers.RevisionProperties{
			ProvisioningState: convert.RefOf(armappcontainers.RevisionProvisioningStateProvisioned),
			HealthState:       convert.RefOf(armappcontainers.RevisionHealthStateHealthy),
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockGetRevisionRequest := mockazsdk.MockContainerAppRevisionGet(
		mockContext,
		subscriptionId,
		resourceGroup,
		appName,
		revisionName,
		expectedRevision,
	)

	cas := NewContainerAppService(mockContext.SubscriptionCredentialProvider, mockContext.HttpClient, clock.NewMock())
	revision, err := cas.ValidateRevision(*mockContext.Context, subscriptionId, resourceGroup, appName, revisionName)
	require.NotNil(t, revision)
	require.NoError(t, err)
	require.Equal(t, expectedRevision, revision)

	expectedGetRevisionPath := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s/revisions/%s",
		subscriptionId,
		resourceGroup,
		appName,
		revisionName,
	)

	require.Equal(t, expectedGetRevisionPath, mockGetRevisionRequest.URL.Path)
}

func Test_ContainerApp_Get(t *testing.T) {
	subscriptionId := "SUBSCRIPTION_ID"
	resourceGroup := "RESOURCE_GROUP"
	appName := "APP_NAME"

	expected := &armappcontainers.ContainerApp{
		Name: &appName,
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockGetContainerAppRequest := mockazsdk.MockContainerAppGet(
		mockContext,
		subscriptionId,
		resourceGroup,
		appName,
		expected,
	)

	cas := NewContainerAppService(mockContext.SubscriptionCredentialProvider, mockContext.HttpClient, clock.NewMock())
	containerApp, err := cas.App(*mockContext.Context, subscriptionId, resourceGroup, appName)
	require.NotNil(t, expected)
	require.NoError(t, err)
	require.Equal(t, expected, containerApp)

	expectedGetContainerAppPath := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s",
		subscriptionId,
		resourceGroup,
		appName,
	)

	require.Equal(t, expectedGetContainerAppPath, mockGetContainerAppRequest.URL.Path)
}

func Test_ContainerApp_ShiftTraffic(t *testing.T) {
	subscriptionId := "SUBSCRIPTION_ID"
	resourceGroup := "RESOURCE_GROUP"
	appName := "APP_NAME"
	revisionName := "NEW_REVISION"

	containerApp := &armappcontainers.ContainerApp{
		Name: &appName,
		Properties: &armappcontainers.ContainerAppProperties{
			Configuration: &armappcontainers.Configuration{
				Ingress: &armappcontainers.Ingress{},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	_ = mockazsdk.MockContainerAppGet(
		mockContext,
		subscriptionId,
		resourceGroup,
		appName,
		containerApp,
	)

	mockContainerAppUpdateRequest := mockazsdk.MockContainerAppUpdate(
		mockContext,
		subscriptionId,
		resourceGroup,
		appName,
		containerApp,
	)

	cas := NewContainerAppService(mockContext.SubscriptionCredentialProvider, mockContext.HttpClient, clock.NewMock())
	err := cas.ShiftTrafficToRevision(
		*mockContext.Context,
		subscriptionId,
		resourceGroup,
		appName,
		revisionName,
	)
	require.NoError(t, err)

	expectedUpdatePath := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s",
		subscriptionId,
		resourceGroup,
		appName,
	)

	requestContainerApp, err := readRawBody[armappcontainers.ContainerApp](mockContainerAppUpdateRequest.Body)
	require.NoError(t, err)

	expectedTrafficWeights := []*armappcontainers.TrafficWeight{
		{
			RevisionName: &revisionName,
			Weight:       convert.RefOf[int32](100),
		},
	}

	require.Equal(t, expectedTrafficWeights, requestContainerApp.Properties.Configuration.Ingress.Traffic)
	require.Equal(t, expectedUpdatePath, mockContainerAppUpdateRequest.URL.Path)
	require.Equal(t, http.MethodPatch, mockContainerAppUpdateRequest.Method)
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

	cas := NewContainerAppService(mockContext.SubscriptionCredentialProvider, mockContext.HttpClient, clock.NewMock())
	revision, err := cas.AddRevision(*mockContext.Context, subscriptionId, resourceGroup, appName, updatedImageName)
	require.NotNil(t, revision)
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

func readRawBody[T any](body io.ReadCloser) (*T, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}

	instance := new(T)

	err = json.Unmarshal(data, instance)
	if err != nil {
		return nil, fmt.Errorf("failed unmarshalling JSON from response: %w", err)
	}

	return instance, nil
}

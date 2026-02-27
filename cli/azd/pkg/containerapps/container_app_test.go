// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package containerapps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
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
				ActiveRevisionsMode: to.Ptr(armappcontainers.ActiveRevisionsModeSingle),
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
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)
	ingressConfig, err := cas.GetIngressConfiguration(*mockContext.Context, subscriptionId, resourceGroup, appName, nil)
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

	containerApp := &armappcontainers.ContainerApp{
		Location: &location,
		Name:     &appName,
		Properties: &armappcontainers.ContainerAppProperties{
			LatestRevisionName: &originalRevisionName,
			Configuration: &armappcontainers.Configuration{
				ActiveRevisionsMode: to.Ptr(armappcontainers.ActiveRevisionsModeSingle),
				Secrets: []*armappcontainers.Secret{
					{
						Name:  to.Ptr("secret"),
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

	secrets := &armappcontainers.SecretsCollection{
		Value: []*armappcontainers.ContainerAppSecret{
			{
				Name:  to.Ptr("secret"),
				Value: to.Ptr("value"),
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	_ = mockazsdk.MockContainerAppGet(mockContext, subscriptionId, resourceGroup, appName, containerApp)
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
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)
	err := cas.AddRevision(*mockContext.Context, subscriptionId, resourceGroup, appName, updatedImageName, nil, nil)
	require.NoError(t, err)

	// Verify container image is updated
	var updatedContainerApp *armappcontainers.ContainerApp
	jsonDecoder := json.NewDecoder(updateContainerAppRequest.Body)
	err = jsonDecoder.Decode(&updatedContainerApp)
	require.NoError(t, err)
	require.Equal(t, updatedImageName, *updatedContainerApp.Properties.Template.Containers[0].Image)
	require.Equal(t, "azd-0", *updatedContainerApp.Properties.Template.RevisionSuffix)
}

func Test_ContainerApp_AddRevision_MultipleRevisionMode(t *testing.T) {
	subscriptionId := "SUBSCRIPTION_ID"
	location := "eastus2"
	resourceGroup := "RESOURCE_GROUP"
	appName := "APP_NAME"
	originalImageName := "ORIGINAL_IMAGE_NAME"
	updatedImageName := "UPDATED_IMAGE_NAME"

	containerApp := &armappcontainers.ContainerApp{
		Location: &location,
		Name:     &appName,
		Properties: &armappcontainers.ContainerAppProperties{
			Configuration: &armappcontainers.Configuration{
				ActiveRevisionsMode: to.Ptr(armappcontainers.ActiveRevisionsModeMultiple),
				Secrets: []*armappcontainers.Secret{
					{
						Name:  to.Ptr("secret"),
						Value: nil,
					},
				},
				Ingress: &armappcontainers.Ingress{},
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

	secrets := &armappcontainers.SecretsCollection{
		Value: []*armappcontainers.ContainerAppSecret{
			{
				Name:  to.Ptr("secret"),
				Value: to.Ptr("value"),
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	_ = mockazsdk.MockContainerAppGet(mockContext, subscriptionId, resourceGroup, appName, containerApp)
	_ = mockazsdk.MockContainerAppSecretsList(mockContext, subscriptionId, resourceGroup, appName, secrets)

	updateCallCount := 0
	updateContainerAppRequest := &http.Request{}
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPatch &&
			strings.Contains(request.URL.Path, fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s",
				subscriptionId,
				resourceGroup,
				appName,
			))
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		updateCallCount++
		*updateContainerAppRequest = *request

		response := armappcontainers.ContainerAppsClientUpdateResponse{}
		return mocks.CreateHttpResponseWithBody(request, http.StatusAccepted, response)
	})

	cas := NewContainerAppService(
		mockContext.SubscriptionCredentialProvider,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)
	err := cas.AddRevision(*mockContext.Context, subscriptionId, resourceGroup, appName, updatedImageName, nil, nil)
	require.NoError(t, err)
	require.Equal(t, 1, updateCallCount)

	var updatedContainerApp *armappcontainers.ContainerApp
	err = mocks.ReadHttpBody(updateContainerAppRequest.Body, &updatedContainerApp)
	require.NoError(t, err)
	require.Equal(t, updatedImageName, *updatedContainerApp.Properties.Template.Containers[0].Image)
	require.Equal(t, "azd-0", *updatedContainerApp.Properties.Template.RevisionSuffix)
	require.NotNil(t, updatedContainerApp.Properties.Configuration.Ingress)
	require.Len(t, updatedContainerApp.Properties.Configuration.Ingress.Traffic, 1)
	expectedRevName := fmt.Sprintf("%s--azd-0", appName)
	require.Equal(t, expectedRevName,
		*updatedContainerApp.Properties.Configuration.Ingress.Traffic[0].RevisionName)
	require.Equal(t, int32(100), *updatedContainerApp.Properties.Configuration.Ingress.Traffic[0].Weight)
}

func Test_ContainerApp_AddRevision_WithEnvVars(t *testing.T) {
	subscriptionId := "SUBSCRIPTION_ID"
	location := "eastus2"
	resourceGroup := "RESOURCE_GROUP"
	appName := "APP_NAME"
	originalImageName := "ORIGINAL_IMAGE_NAME"
	updatedImageName := "UPDATED_IMAGE_NAME"
	existingValue := "existing-value"
	overrideValue := "old-value"
	newOverrideValue := "new-value"
	newValue := "brand-new-value"

	containerApp := &armappcontainers.ContainerApp{
		Location: &location,
		Name:     &appName,
		Properties: &armappcontainers.ContainerAppProperties{
			Configuration: &armappcontainers.Configuration{
				ActiveRevisionsMode: to.Ptr(armappcontainers.ActiveRevisionsModeSingle),
			},
			Template: &armappcontainers.Template{
				Containers: []*armappcontainers.Container{
					{
						Image: &originalImageName,
						Env: []*armappcontainers.EnvironmentVar{
							{
								Name:  to.Ptr("EXISTING"),
								Value: &existingValue,
							},
							{
								Name:  to.Ptr("OVERRIDE"),
								Value: &overrideValue,
							},
						},
					},
				},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	_ = mockazsdk.MockContainerAppGet(mockContext, subscriptionId, resourceGroup, appName, containerApp)
	updateContainerAppRequest := mockazsdk.MockContainerAppUpdate(
		mockContext,
		subscriptionId,
		resourceGroup,
		appName,
		containerApp,
	)

	cas := NewContainerAppService(
		mockContext.SubscriptionCredentialProvider,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)

	err := cas.AddRevision(*mockContext.Context, subscriptionId, resourceGroup, appName, updatedImageName, map[string]string{
		"OVERRIDE": newOverrideValue,
		"NEW":      newValue,
	}, nil)
	require.NoError(t, err)

	var updatedContainerApp *armappcontainers.ContainerApp
	err = mocks.ReadHttpBody(updateContainerAppRequest.Body, &updatedContainerApp)
	require.NoError(t, err)
	require.Equal(t, updatedImageName, *updatedContainerApp.Properties.Template.Containers[0].Image)

	actualEnv := map[string]string{}
	for _, envVar := range updatedContainerApp.Properties.Template.Containers[0].Env {
		require.NotNil(t, envVar.Name)
		require.NotNil(t, envVar.Value)
		actualEnv[*envVar.Name] = *envVar.Value
	}

	require.Equal(t, map[string]string{
		"EXISTING": existingValue,
		"OVERRIDE": newOverrideValue,
		"NEW":      newValue,
	}, actualEnv)
}

func Test_ContainerApp_DeployYaml(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	subscriptionId := "SUBSCRIPTION_ID"
	location := "eastus2"
	resourceGroup := "RESOURCE_GROUP"
	appName := "APP_NAME"

	containerAppYaml := `
location: eastus2
name: APP_NAME
properties:
  latestRevisionName: LATEST_REVISION_NAME
  configuration:
    activeRevisionsMode: Single
  template:
    containers:
      - image: IMAGE_NAME
`

	expected := &armappcontainers.ContainerApp{
		Location: to.Ptr(location),
		Name:     to.Ptr(appName),
		Properties: &armappcontainers.ContainerAppProperties{
			LatestRevisionName: to.Ptr("LATEST_REVISION_NAME"),
			Configuration: &armappcontainers.Configuration{
				ActiveRevisionsMode: to.Ptr(armappcontainers.ActiveRevisionsModeSingle),
				Ingress: &armappcontainers.Ingress{
					CustomDomains: []*armappcontainers.CustomDomain{
						{
							Name: to.Ptr("DOMAIN_NAME"),
						},
					},
					StickySessions: &armappcontainers.IngressStickySessions{
						Affinity: to.Ptr(armappcontainers.AffinitySticky),
					},
				},
			},
			Template: &armappcontainers.Template{
				Containers: []*armappcontainers.Container{
					{
						Image: to.Ptr("IMAGE_NAME"),
					},
				},
			},
		},
	}

	containerAppGetRequest := mockazsdk.MockContainerAppGet(
		mockContext,
		subscriptionId,
		resourceGroup,
		appName,
		expected,
	)
	require.NotNil(t, containerAppGetRequest)

	containerAppUpdateRequest := mockazsdk.MockContainerAppCreateOrUpdate(
		mockContext,
		subscriptionId,
		resourceGroup,
		appName,
		expected,
	)
	require.NotNil(t, containerAppUpdateRequest)

	cas := NewContainerAppService(
		mockContext.SubscriptionCredentialProvider,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)

	err := mockContext.Config.Set("alpha.aca.persistDomains", "on")
	require.NoError(t, err)
	err = mockContext.Config.Set("alpha.aca.persistIngressSessionAffinity", "on")
	require.NoError(t, err)

	err = cas.DeployYaml(*mockContext.Context, subscriptionId, resourceGroup, appName, []byte(containerAppYaml), nil)
	require.NoError(t, err)

	var actual *armappcontainers.ContainerApp
	err = mocks.ReadHttpBody(containerAppUpdateRequest.Body, &actual)
	require.NoError(t, err)

	require.Equal(t, expected.Properties.Configuration, actual.Properties.Configuration)
	require.Equal(t, expected.Properties.Template, actual.Properties.Template)
}

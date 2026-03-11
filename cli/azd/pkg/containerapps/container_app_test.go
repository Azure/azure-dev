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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
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

func Test_ContainerAppJob_Get(t *testing.T) {
	subscriptionId := "SUBSCRIPTION_ID"
	resourceGroup := "RESOURCE_GROUP"
	jobName := "MY_JOB"
	location := "eastus2"
	imageName := "myregistry.azurecr.io/myimage:latest"

	job := &armappcontainers.Job{
		Location: to.Ptr(location),
		Name:     to.Ptr(jobName),
		Properties: &armappcontainers.JobProperties{
			Template: &armappcontainers.JobTemplate{
				Containers: []*armappcontainers.Container{
					{
						Name:  to.Ptr(jobName),
						Image: to.Ptr(imageName),
					},
				},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockRequest := mockazsdk.MockContainerAppJobGet(
		mockContext, subscriptionId, resourceGroup, jobName, job,
	)

	cas := NewContainerAppService(
		mockContext.SubscriptionCredentialProvider,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)

	result, err := cas.GetContainerAppJob(
		*mockContext.Context, subscriptionId, resourceGroup, jobName, nil,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, jobName, *result.Name)
	require.Equal(t, location, *result.Location)
	require.Equal(t, imageName, *result.Properties.Template.Containers[0].Image)

	expectedPath := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/jobs/%s",
		subscriptionId, resourceGroup, jobName,
	)
	require.Equal(t, expectedPath, mockRequest.URL.Path)
}

func Test_ContainerAppJob_UpdateImage(t *testing.T) {
	subscriptionId := "SUBSCRIPTION_ID"
	resourceGroup := "RESOURCE_GROUP"
	jobName := "MY_JOB"
	location := "eastus2"
	originalImage := "myregistry.azurecr.io/myimage:v1"
	updatedImage := "myregistry.azurecr.io/myimage:v2"

	job := &armappcontainers.Job{
		Location: to.Ptr(location),
		Name:     to.Ptr(jobName),
		Properties: &armappcontainers.JobProperties{
			Template: &armappcontainers.JobTemplate{
				Containers: []*armappcontainers.Container{
					{
						Name:  to.Ptr(jobName),
						Image: to.Ptr(originalImage),
					},
				},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	_ = mockazsdk.MockContainerAppJobGet(
		mockContext, subscriptionId, resourceGroup, jobName, job,
	)
	mockUpdate := mockazsdk.MockContainerAppJobUpdate(
		mockContext, subscriptionId, resourceGroup, jobName, job,
	)

	cas := NewContainerAppService(
		mockContext.SubscriptionCredentialProvider,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)

	err := cas.UpdateContainerAppJobImage(
		*mockContext.Context, subscriptionId, resourceGroup,
		jobName, updatedImage, nil, nil,
	)
	require.NoError(t, err)

	// Verify the PATCH body contains the updated image
	var patchBody armappcontainers.JobPatchProperties
	err = mocks.ReadHttpBody(mockUpdate.Body, &patchBody)
	require.NoError(t, err)
	require.NotNil(t, patchBody.Properties)
	require.NotNil(t, patchBody.Properties.Template)
	require.Len(t, patchBody.Properties.Template.Containers, 1)
	require.Equal(t, updatedImage, *patchBody.Properties.Template.Containers[0].Image)
}

func Test_ContainerAppJob_UpdateImage_NilContainers(t *testing.T) {
	subscriptionId := "SUBSCRIPTION_ID"
	resourceGroup := "RESOURCE_GROUP"
	jobName := "MY_JOB"

	job := &armappcontainers.Job{
		Name:       to.Ptr(jobName),
		Properties: nil,
	}

	mockContext := mocks.NewMockContext(context.Background())
	_ = mockazsdk.MockContainerAppJobGet(
		mockContext, subscriptionId, resourceGroup, jobName, job,
	)
	_ = mockazsdk.MockContainerAppJobUpdate(
		mockContext, subscriptionId, resourceGroup, jobName, job,
	)

	cas := NewContainerAppService(
		mockContext.SubscriptionCredentialProvider,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)

	err := cas.UpdateContainerAppJobImage(
		*mockContext.Context, subscriptionId, resourceGroup,
		jobName, "new-image:latest", nil, nil,
	)
	require.Error(t, err)
}

func Test_ContainerAppJob_CreateJobsClient_CacheHit(t *testing.T) {
	subscriptionId := "SUBSCRIPTION_ID"
	resourceGroup := "RESOURCE_GROUP"
	jobName := "MY_JOB"
	location := "eastus2"
	imageName := "myregistry.azurecr.io/myimage:latest"

	job := &armappcontainers.Job{
		Location: to.Ptr(location),
		Name:     to.Ptr(jobName),
		Properties: &armappcontainers.JobProperties{
			Template: &armappcontainers.JobTemplate{
				Containers: []*armappcontainers.Container{
					{
						Name:  to.Ptr(jobName),
						Image: to.Ptr(imageName),
					},
				},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	_ = mockazsdk.MockContainerAppJobGet(
		mockContext, subscriptionId, resourceGroup, jobName, job,
	)

	cas := NewContainerAppService(
		mockContext.SubscriptionCredentialProvider,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)

	// First call — creates and caches the jobs client
	result1, err := cas.GetContainerAppJob(
		*mockContext.Context, subscriptionId, resourceGroup,
		jobName, nil,
	)
	require.NoError(t, err)
	require.NotNil(t, result1)
	require.Equal(t, jobName, *result1.Name)

	// Second call with same subscriptionId — hits the cache
	result2, err := cas.GetContainerAppJob(
		*mockContext.Context, subscriptionId, resourceGroup,
		jobName, nil,
	)
	require.NoError(t, err)
	require.NotNil(t, result2)
	require.Equal(t, jobName, *result2.Name)
}

func Test_ContainerAppJob_Get_CredentialError(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	credErr := fmt.Errorf("credential unavailable")
	failingProvider := mockaccount.SubscriptionCredentialProviderFunc(
		func(
			ctx context.Context, subscriptionId string,
		) (azcore.TokenCredential, error) {
			return nil, credErr
		},
	)

	cas := NewContainerAppService(
		failingProvider,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)

	_, err := cas.GetContainerAppJob(
		*mockContext.Context, "SUB", "RG", "JOB", nil,
	)
	require.Error(t, err)
	require.ErrorIs(t, err, credErr)
}

func Test_ContainerAppJob_Get_Error(t *testing.T) {
	subscriptionId := "SUBSCRIPTION_ID"
	resourceGroup := "RESOURCE_GROUP"
	jobName := "MISSING_JOB"

	mockContext := mocks.NewMockContext(context.Background())

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.Path,
				fmt.Sprintf(
					"/subscriptions/%s/resourceGroups/%s"+
						"/providers/Microsoft.App/jobs/%s",
					subscriptionId,
					resourceGroup,
					jobName,
				),
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(
			request, http.StatusNotFound,
		)
	})

	cas := NewContainerAppService(
		mockContext.SubscriptionCredentialProvider,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)

	_, err := cas.GetContainerAppJob(
		*mockContext.Context, subscriptionId, resourceGroup,
		jobName, nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "getting container app job")
}

func Test_ContainerAppJob_UpdateImage_GetError(t *testing.T) {
	subscriptionId := "SUBSCRIPTION_ID"
	resourceGroup := "RESOURCE_GROUP"
	jobName := "MISSING_JOB"

	mockContext := mocks.NewMockContext(context.Background())

	// Mock GET to return 404 so the internal GetContainerAppJob
	// call inside UpdateContainerAppJobImage fails.
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.Path,
				fmt.Sprintf(
					"/subscriptions/%s/resourceGroups/%s"+
						"/providers/Microsoft.App/jobs/%s",
					subscriptionId,
					resourceGroup,
					jobName,
				),
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(
			request, http.StatusNotFound,
		)
	})

	cas := NewContainerAppService(
		mockContext.SubscriptionCredentialProvider,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)

	err := cas.UpdateContainerAppJobImage(
		*mockContext.Context, subscriptionId, resourceGroup,
		jobName, "new-image:v2", nil, nil,
	)
	require.Error(t, err)
	require.Contains(
		t, err.Error(), "getting container app job for update",
	)
}

func Test_ContainerAppJob_UpdateImage_CustomApiVersion(t *testing.T) {
	subscriptionId := "SUBSCRIPTION_ID"
	resourceGroup := "RESOURCE_GROUP"
	jobName := "MY_JOB"
	location := "eastus2"
	originalImage := "myregistry.azurecr.io/myimage:v1"
	updatedImage := "myregistry.azurecr.io/myimage:v3"
	customApiVersion := "2024-10-02-preview"

	job := &armappcontainers.Job{
		Location: to.Ptr(location),
		Name:     to.Ptr(jobName),
		Properties: &armappcontainers.JobProperties{
			Template: &armappcontainers.JobTemplate{
				Containers: []*armappcontainers.Container{
					{
						Name:  to.Ptr(jobName),
						Image: to.Ptr(originalImage),
					},
				},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())

	// Custom mock for GET that verifies the custom api-version
	// query parameter is set.
	var capturedGetApiVersion string
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.Path,
				fmt.Sprintf(
					"/subscriptions/%s/resourceGroups/%s"+
						"/providers/Microsoft.App/jobs/%s",
					subscriptionId,
					resourceGroup,
					jobName,
				),
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		capturedGetApiVersion = request.URL.Query().Get(
			"api-version",
		)
		response := armappcontainers.JobsClientGetResponse{
			Job: *job,
		}
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusOK, response,
		)
	})

	// Custom mock for PATCH that captures the api-version
	var capturedPatchApiVersion string
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPatch &&
			strings.Contains(
				request.URL.Path,
				fmt.Sprintf(
					"/subscriptions/%s/resourceGroups/%s"+
						"/providers/Microsoft.App/jobs/%s",
					subscriptionId,
					resourceGroup,
					jobName,
				),
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		capturedPatchApiVersion = request.URL.Query().Get(
			"api-version",
		)
		response := armappcontainers.JobsClientUpdateResponse{}
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusAccepted, response,
		)
	})

	cas := NewContainerAppService(
		mockContext.SubscriptionCredentialProvider,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)

	err := cas.UpdateContainerAppJobImage(
		*mockContext.Context, subscriptionId, resourceGroup,
		jobName, updatedImage, nil,
		&ContainerAppOptions{ApiVersion: customApiVersion},
	)
	require.NoError(t, err)

	// Verify the custom api-version was injected into both
	// the GET and PATCH requests.
	require.Equal(t, customApiVersion, capturedGetApiVersion)
	require.Equal(t, customApiVersion, capturedPatchApiVersion)
}

func Test_ContainerAppJob_UpdateImage_EmptyImage(t *testing.T) {
	subscriptionId := "SUBSCRIPTION_ID"
	resourceGroup := "RESOURCE_GROUP"
	jobName := "MY_JOB"

	mockContext := mocks.NewMockContext(context.Background())

	cas := NewContainerAppService(
		mockContext.SubscriptionCredentialProvider,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)

	err := cas.UpdateContainerAppJobImage(
		*mockContext.Context, subscriptionId, resourceGroup,
		jobName, "", nil, nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "must not be empty")
}

func Test_ContainerAppJob_UpdateImage_NilContainerElement(t *testing.T) {
	subscriptionId := "SUBSCRIPTION_ID"
	resourceGroup := "RESOURCE_GROUP"
	jobName := "MY_JOB"

	job := &armappcontainers.Job{
		Name: to.Ptr(jobName),
		Properties: &armappcontainers.JobProperties{
			Template: &armappcontainers.JobTemplate{
				Containers: []*armappcontainers.Container{
					nil,
				},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	_ = mockazsdk.MockContainerAppJobGet(
		mockContext, subscriptionId, resourceGroup, jobName, job,
	)
	_ = mockazsdk.MockContainerAppJobUpdate(
		mockContext, subscriptionId, resourceGroup, jobName, job,
	)

	cas := NewContainerAppService(
		mockContext.SubscriptionCredentialProvider,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)

	err := cas.UpdateContainerAppJobImage(
		*mockContext.Context, subscriptionId, resourceGroup,
		jobName, "new-image:latest", nil, nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil container entry")
}

// Test_ContainerApp_DeployYaml_PreservesDaprConfig verifies that when a deployment YAML does not include
// Dapr configuration, any existing Dapr configuration on the container app is preserved.
// This ensures that Dapr configuration set externally (e.g. via Terraform) is not removed on deploy.
func Test_ContainerApp_DeployYaml_PreservesDaprConfig(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	subscriptionId := "SUBSCRIPTION_ID"
	location := "eastus2"
	resourceGroup := "RESOURCE_GROUP"
	appName := "APP_NAME"

	// YAML does NOT include Dapr configuration
	containerAppYaml := `
location: eastus2
name: APP_NAME
properties:
  configuration:
    activeRevisionsMode: Single
  template:
    containers:
      - image: IMAGE_NAME
`

	// Existing container app has Dapr enabled
	existingApp := &armappcontainers.ContainerApp{
		Location: to.Ptr(location),
		Name:     to.Ptr(appName),
		Properties: &armappcontainers.ContainerAppProperties{
			Configuration: &armappcontainers.Configuration{
				ActiveRevisionsMode: to.Ptr(armappcontainers.ActiveRevisionsModeSingle),
				Dapr: &armappcontainers.Dapr{
					AppID:   to.Ptr("my-app"),
					AppPort: to.Ptr[int32](8080),
					Enabled: to.Ptr(true),
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

	_ = mockazsdk.MockContainerAppGet(mockContext, subscriptionId, resourceGroup, appName, existingApp)
	containerAppUpdateRequest := mockazsdk.MockContainerAppCreateOrUpdate(
		mockContext, subscriptionId, resourceGroup, appName, existingApp,
	)

	cas := NewContainerAppService(
		mockContext.SubscriptionCredentialProvider,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)

	err := cas.DeployYaml(*mockContext.Context, subscriptionId, resourceGroup, appName, []byte(containerAppYaml), nil)
	require.NoError(t, err)

	var actual *armappcontainers.ContainerApp
	err = mocks.ReadHttpBody(containerAppUpdateRequest.Body, &actual)
	require.NoError(t, err)

	// Dapr configuration should be preserved from the existing container app
	require.NotNil(t, actual.Properties.Configuration.Dapr)
	require.Equal(t, "my-app", *actual.Properties.Configuration.Dapr.AppID)
	require.Equal(t, int32(8080), *actual.Properties.Configuration.Dapr.AppPort)
	require.Equal(t, true, *actual.Properties.Configuration.Dapr.Enabled)
}

// Test_ContainerApp_DeployYaml_YamlDaprConfigNotOverridden verifies that when a deployment YAML already
// includes Dapr configuration, the YAML's Dapr configuration is used (not the existing app's configuration).
func Test_ContainerApp_DeployYaml_YamlDaprConfigNotOverridden(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	subscriptionId := "SUBSCRIPTION_ID"
	location := "eastus2"
	resourceGroup := "RESOURCE_GROUP"
	appName := "APP_NAME"

	// YAML includes its own Dapr configuration
	containerAppYaml := `
location: eastus2
name: APP_NAME
properties:
  configuration:
    activeRevisionsMode: Single
    dapr:
      appId: yaml-app
      appPort: 9090
      enabled: true
  template:
    containers:
      - image: IMAGE_NAME
`

	// Existing container app has different Dapr configuration
	existingApp := &armappcontainers.ContainerApp{
		Location: to.Ptr(location),
		Name:     to.Ptr(appName),
		Properties: &armappcontainers.ContainerAppProperties{
			Configuration: &armappcontainers.Configuration{
				ActiveRevisionsMode: to.Ptr(armappcontainers.ActiveRevisionsModeSingle),
				Dapr: &armappcontainers.Dapr{
					AppID:   to.Ptr("existing-app"),
					AppPort: to.Ptr[int32](8080),
					Enabled: to.Ptr(true),
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

	_ = mockazsdk.MockContainerAppGet(mockContext, subscriptionId, resourceGroup, appName, existingApp)
	containerAppUpdateRequest := mockazsdk.MockContainerAppCreateOrUpdate(
		mockContext, subscriptionId, resourceGroup, appName, existingApp,
	)

	cas := NewContainerAppService(
		mockContext.SubscriptionCredentialProvider,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)

	err := cas.DeployYaml(*mockContext.Context, subscriptionId, resourceGroup, appName, []byte(containerAppYaml), nil)
	require.NoError(t, err)

	var actual *armappcontainers.ContainerApp
	err = mocks.ReadHttpBody(containerAppUpdateRequest.Body, &actual)
	require.NoError(t, err)

	// Dapr configuration from the YAML should be used, not the existing app's
	require.NotNil(t, actual.Properties.Configuration.Dapr)
	require.Equal(t, "yaml-app", *actual.Properties.Configuration.Dapr.AppID)
	require.Equal(t, int32(9090), *actual.Properties.Configuration.Dapr.AppPort)
}

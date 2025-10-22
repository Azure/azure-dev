// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/containerapps"
	"github.com/azure/azure-dev/cli/azd/pkg/containerregistry"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazsdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/require"
)

func TestNewContainerAppTargetTypeValidation(t *testing.T) {
	t.Parallel()

	tests := map[string]*serviceTargetValidationTest{
		"ValidateTypeSuccess": {
			targetResource: environment.NewTargetResource(
				"SUB_ID",
				"RG_ID",
				"res",
				string(azapi.AzureResourceTypeContainerApp),
			),
			expectError: false,
		},
		"ValidateTypeLowerCaseSuccess": {
			targetResource: environment.NewTargetResource(
				"SUB_ID",
				"RG_ID",
				"res",
				strings.ToLower(string(azapi.AzureResourceTypeContainerApp)),
			),
			expectError: false,
		},
		"ValidateTypeFail": {
			targetResource: environment.NewTargetResource("SUB_ID", "RG_ID", "res", "BadType"),
			expectError:    true,
		},
	}

	for test, data := range tests {
		t.Run(test, func(t *testing.T) {
			serviceTarget := &containerAppTarget{}

			err := serviceTarget.validateTargetResource(data.targetResource)
			if data.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func Test_ContainerApp_Deploy(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForContainerAppTarget(mockContext)

	serviceConfig := createTestServiceConfig(tempDir, ContainerAppTarget, ServiceLanguageTypeScript)
	env := createEnv()

	serviceTarget := createContainerAppServiceTarget(mockContext, env)

	packageResult, err := logProgress(
		t, func(progress *async.Progress[ServiceProgress]) (*ServicePackageResult, error) {
			serviceContext := NewServiceContext()
			serviceContext.Package = ArtifactCollection{
				{
					Kind:         ArtifactKindContainer,
					Location:     "test-app/api-test:azd-deploy-0",
					LocationKind: LocationKindRemote,
					Metadata: map[string]string{
						"imageHash":   "IMAGE_HASH",
						"targetImage": "test-app/api-test:azd-deploy-0",
					},
				},
			}
			return serviceTarget.Package(
				*mockContext.Context,
				serviceConfig,
				serviceContext,
				progress,
			)
		},
	)

	require.NoError(t, err)
	require.NotNil(t, packageResult)
	require.Len(t, packageResult.Artifacts, 0)

	scope := environment.NewTargetResource(
		"SUBSCRIPTION_ID",
		"RESOURCE_GROUP",
		"CONTAINER_APP",
		string(azapi.AzureResourceTypeContainerApp),
	)

	// Create a mock publish result that would normally come from a Publish operation
	publishResult := &ServicePublishResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindContainer,
				Location:     "REGISTRY.azurecr.io/test-app/api-test:azd-deploy-0",
				LocationKind: LocationKindRemote,
				Metadata: map[string]string{
					"remoteImage": "REGISTRY.azurecr.io/test-app/api-test:azd-deploy-0",
				},
			},
		},
	}

	deployResult, err := logProgress(
		t, func(progress *async.Progress[ServiceProgress]) (*ServiceDeployResult, error) {
			serviceContext := NewServiceContext()
			serviceContext.Package = packageResult.Artifacts
			serviceContext.Publish = publishResult.Artifacts
			return serviceTarget.Deploy(*mockContext.Context, serviceConfig, serviceContext, scope, progress)
		},
	)

	require.NoError(t, err)
	require.NotNil(t, deployResult)
	require.Greater(t, len(deployResult.Artifacts), 0)

	// Verify we have deployment artifacts
	deployArtifacts := deployResult.Artifacts.Find()
	require.Greater(t, len(deployArtifacts), 0)
}

func Test_ContainerApp_Publish(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	mockContext := mocks.NewMockContext(context.Background())
	setupMocksForContainerAppTarget(mockContext)

	serviceConfig := createTestServiceConfig(tempDir, ContainerAppTarget, ServiceLanguageTypeScript)
	env := createEnv()

	serviceTarget := createContainerAppServiceTarget(mockContext, env)

	serviceContext := NewServiceContext()
	serviceContext.Package = ArtifactCollection{
		{
			Kind:         ArtifactKindContainer,
			Location:     "test-app/api-test:azd-deploy-0",
			LocationKind: LocationKindRemote,
			Metadata: map[string]string{
				"imageHash":   "IMAGE_HASH",
				"targetImage": "test-app/api-test:azd-deploy-0",
			},
		},
	}

	packageResult, err := logProgress(
		t, func(progress *async.Progress[ServiceProgress]) (*ServicePackageResult, error) {
			return serviceTarget.Package(
				*mockContext.Context,
				serviceConfig,
				serviceContext,
				progress,
			)
		},
	)
	require.NoError(t, err)
	require.NotNil(t, packageResult)
	require.Len(t, packageResult.Artifacts, 0)

	scope := environment.NewTargetResource(
		"SUBSCRIPTION_ID",
		"RESOURCE_GROUP",
		"CONTAINER_APP",
		string(azapi.AzureResourceTypeContainerApp),
	)

	publishResult, err := logProgress(
		t, func(progress *async.Progress[ServiceProgress]) (*ServicePublishResult, error) {
			return serviceTarget.Publish(
				*mockContext.Context, serviceConfig, serviceContext, scope, progress, &PublishOptions{})
		},
	)
	require.NoError(t, err)
	require.NotNil(t, publishResult)
	require.Len(t, publishResult.Artifacts, 1)

	// Verify the environment variable was set correctly
	require.Equal(t, "REGISTRY.azurecr.io/test-app/api-test:azd-deploy-0", env.Dotenv()["SERVICE_API_IMAGE_NAME"])

	// Verify the publish result contains the expected image location
	publishArtifacts := publishResult.Artifacts.Find()
	require.Greater(t, len(publishArtifacts), 0)
	require.Equal(t, "REGISTRY.azurecr.io/test-app/api-test:azd-deploy-0", publishArtifacts[0].Location)
}

func createContainerAppServiceTarget(
	mockContext *mocks.MockContext,
	env *environment.Environment,
) ServiceTarget {
	dockerCli := docker.NewCli(mockContext.CommandRunner)
	dotnetCli := dotnet.NewCli(mockContext.CommandRunner)
	credentialProvider := mockaccount.SubscriptionCredentialProviderFunc(
		func(_ context.Context, _ string) (azcore.TokenCredential, error) {
			return mockContext.Credentials, nil
		})

	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", *mockContext.Context, env).Return(nil)

	containerAppService := containerapps.NewContainerAppService(
		credentialProvider,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)
	containerRegistryService := azapi.NewContainerRegistryService(
		credentialProvider,
		dockerCli,
		mockContext.ArmClientOptions,
		mockContext.CoreClientOptions,
	)
	remoteBuildManager := containerregistry.NewRemoteBuildManager(
		credentialProvider,
		mockContext.ArmClientOptions,
	)
	containerHelper := NewContainerHelper(
		env,
		envManager,
		clock.NewMock(),
		containerRegistryService,
		remoteBuildManager,
		nil,
		dockerCli,
		dotnetCli,
		mockContext.Console,
		cloud.AzurePublic(),
	)
	deploymentService := mockazapi.NewStandardDeploymentsFromMockContext(mockContext)
	resourceService := azapi.NewResourceService(credentialProvider, mockContext.ArmClientOptions)
	azureResourceManager := infra.NewAzureResourceManager(resourceService, deploymentService)
	resourceManager := NewResourceManager(env, deploymentService, resourceService, azureResourceManager)

	return NewContainerAppTarget(
		env,
		envManager,
		containerHelper,
		containerAppService,
		resourceManager,
		deploymentService,
		mockContext.Console,
		mockContext.CommandRunner,
	)
}

func setupMocksForContainerAppTarget(mockContext *mocks.MockContext) {
	setupMocksForDocker(mockContext)
	setupMocksForAcr(mockContext)
	setupMocksForContainerApps(mockContext)
}

func setupMocksForContainerApps(mockContext *mocks.MockContext) {
	subscriptionId := "SUBSCRIPTION_ID"
	location := "eastus2"
	resourceGroup := "RESOURCE_GROUP"
	appName := "CONTAINER_APP"
	originalImageName := "ORIGINAL_IMAGE_NAME"
	originalRevisionName := "ORIGINAL_REVISION_NAME"
	updatedRevisionName := "UPDATED_REVISION_NAME"
	hostName := fmt.Sprintf("%s.%s.azurecontainerapps.io", appName, location)

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
				Ingress: &armappcontainers.Ingress{
					Fqdn: &hostName,
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
		Value: []*armappcontainers.ContainerAppSecret{},
	}

	mockazsdk.MockContainerAppGet(mockContext, subscriptionId, resourceGroup, appName, containerApp)
	mockazsdk.MockContainerAppRevisionGet(
		mockContext,
		subscriptionId,
		resourceGroup,
		appName,
		originalRevisionName,
		revision,
	)
	mockazsdk.MockContainerAppSecretsList(mockContext, subscriptionId, resourceGroup, appName, secrets)
	mockazsdk.MockContainerAppUpdate(mockContext, subscriptionId, resourceGroup, appName, containerApp)
	mockazsdk.MockContainerRegistryTokenExchange(mockContext, subscriptionId, subscriptionId, "REFRESH_TOKEN")
}

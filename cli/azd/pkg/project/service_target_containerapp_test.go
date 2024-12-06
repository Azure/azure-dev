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
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
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
			return serviceTarget.Package(
				*mockContext.Context,
				serviceConfig,
				&ServicePackageResult{
					PackagePath: "test-app/api-test:azd-deploy-0",
					Details: &dockerPackageResult{
						ImageHash:   "IMAGE_HASH",
						TargetImage: "test-app/api-test:azd-deploy-0",
					},
				},
				progress,
			)
		},
	)

	require.NoError(t, err)
	require.NotNil(t, packageResult)
	require.IsType(t, new(dockerPackageResult), packageResult.Details)

	scope := environment.NewTargetResource(
		"SUBSCRIPTION_ID",
		"RESOURCE_GROUP",
		"CONTAINER_APP",
		string(azapi.AzureResourceTypeContainerApp),
	)

	deployResult, err := logProgress(
		t, func(progress *async.Progress[ServiceProgress]) (*ServiceDeployResult, error) {
			return serviceTarget.Deploy(*mockContext.Context, serviceConfig, packageResult, scope, progress)
		},
	)

	require.NoError(t, err)
	require.NotNil(t, deployResult)
	require.Equal(t, ContainerAppTarget, deployResult.Kind)
	require.Greater(t, len(deployResult.Endpoints), 0)
	// New env variable is created
	require.Equal(t, "REGISTRY.azurecr.io/test-app/api-test:azd-deploy-0", env.Dotenv()["SERVICE_API_IMAGE_NAME"])
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
	containerRegistryService := azcli.NewContainerRegistryService(
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
		dockerCli,
		dotnetCli,
		mockContext.Console,
		cloud.AzurePublic(),
	)
	deploymentService := mockazcli.NewStandardDeploymentsFromMockContext(mockContext)
	resourceService := azapi.NewResourceService(credentialProvider, mockContext.ArmClientOptions)
	azureResourceManager := infra.NewAzureResourceManager(resourceService, deploymentService)
	resourceManager := NewResourceManager(env, deploymentService, resourceService, azureResourceManager)

	return NewContainerAppTarget(
		env,
		envManager,
		containerHelper,
		containerAppService,
		resourceManager,
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

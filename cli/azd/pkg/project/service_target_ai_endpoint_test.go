// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_MlEndpointTarget_Deploy(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.Clock.Set(time.Now())
	env := environment.NewWithValues("test", map[string]string{
		AiProjectNameEnvVarName:              "AI_WORKSPACE",
		environment.TenantIdEnvVarName:       "TENANT_ID",
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
		environment.ResourceGroupEnvVarName:  "RESOURCE_GROUP",
	})

	endpointName := "MY-ONLINE-ENDPOINT"
	flowName := "MY-FLOW"
	environmentName := "MY-ENVIRONMENT"
	modelName := "MY-MODEL"
	deploymentName := "MY-DEPLOYMENT"
	expectedDeploymentName := fmt.Sprintf("%s-%d", deploymentName, mockContext.Clock.Now().Unix())
	expectedFlowName := fmt.Sprintf("%s-%d", flowName, mockContext.Clock.Now().Unix())

	targetResource := environment.NewTargetResource(
		env.GetSubscriptionId(),
		env.Getenv(environment.ResourceGroupEnvVarName),
		endpointName,
		string(azapi.AzureResourceTypeMachineLearningEndpoint),
	)
	serviceConfig := createTestServiceConfig("./contoso-chat", AiEndpointTarget, ServiceLanguagePython)
	serviceConfig.Config = map[string]any{
		"flow": map[string]any{
			"name": flowName,
			"path": ".",
		},
		"environment": map[string]any{
			"name": environmentName,
			"path": "./deployment/environment.yaml",
		},
		"model": map[string]any{
			"name": modelName,
			"path": "./deployment/model.yaml",
		},
		"deployment": map[string]any{
			"name": deploymentName,
			"path": "./deployment/deployment.yaml",
		},
	}

	flow := &ai.Flow{
		Name:        uuid.New().String(),
		DisplayName: expectedFlowName,
		Path:        "./flow/flow.yaml",
	}

	environmentVersion := &armmachinelearning.EnvironmentVersion{
		Name: to.Ptr("1"),
		Properties: &armmachinelearning.EnvironmentVersionProperties{
			EnvironmentType: to.Ptr(armmachinelearning.EnvironmentTypeUserCreated),
		},
		ID: to.Ptr(
			//nolint:lll
			"/subscriptions/test/resourceGroups/test/providers/Microsoft.MachineLearningServices/workspaces/test/environments/test/versions/1",
		),
		Type: to.Ptr("Microsoft.MachineLearningServices/workspaces/environments/versions"),
	}

	modelVersion := &armmachinelearning.ModelVersion{
		Name: to.Ptr("1"),
		ID: to.Ptr(
			//nolint:lll
			"/subscriptions/test/resourceGroups/test/providers/Microsoft.MachineLearningServices/workspaces/test/models/test/versions/1",
		),
		Type: to.Ptr("Microsoft.MachineLearningServices/workspaces/models/versions"),
		Properties: &armmachinelearning.ModelVersionProperties{
			ModelType: to.Ptr("CustomModel"),
		},
	}

	onlineDeployment := &armmachinelearning.OnlineDeployment{
		Name: &expectedDeploymentName,
		ID: to.Ptr(
			//nolint:lll
			"/subscriptions/test/resourceGroups/test/providers/Microsoft.MachineLearningServices/workspaces/test/onlineEndpoints/test/deployments/" + expectedDeploymentName,
		),
		Type: to.Ptr("Microsoft.MachineLearningServices/workspaces/onlineEndpoints/deployments"),
	}

	onlineEndpoint := &armmachinelearning.OnlineEndpoint{
		Name: to.Ptr(endpointName),
		Properties: &armmachinelearning.OnlineEndpointProperties{
			ScoringURI: to.Ptr("https://SCRORING_URI"),
			SwaggerURI: to.Ptr("https://SWAGGER_URI"),
			Traffic: map[string]*int32{
				deploymentName: to.Ptr(int32(100)),
			},
		},
	}

	scopeType := mock.AnythingOfType("*ai.Scope")
	componentConfigType := mock.AnythingOfType("*ai.ComponentConfig")
	endpointDeploymentConfigType := mock.AnythingOfType("*ai.EndpointDeploymentConfig")

	aiHelper := &mockAiHelper{}
	aiHelper.
		On("Initialize", *mockContext.Context).
		Return(nil)
	aiHelper.
		On("ValidateWorkspace", *mockContext.Context, scopeType).
		Return(nil)
	aiHelper.
		On("CreateFlow", *mockContext.Context, scopeType, serviceConfig, componentConfigType).
		Return(flow, nil)
	aiHelper.
		On("CreateEnvironmentVersion", *mockContext.Context, scopeType, serviceConfig, componentConfigType).
		Return(environmentVersion, nil)
	aiHelper.
		On("CreateModelVersion", *mockContext.Context, scopeType, serviceConfig, componentConfigType).
		Return(modelVersion, nil)
	aiHelper.
		On("DeployToEndpoint", *mockContext.Context, scopeType, serviceConfig, endpointName, endpointDeploymentConfigType).
		Return(onlineDeployment, nil)
	aiHelper.
		On("UpdateTraffic", *mockContext.Context, scopeType, endpointName, expectedDeploymentName).
		Return(onlineEndpoint, nil)
	aiHelper.
		On("DeleteDeployments", *mockContext.Context, scopeType, endpointName).
		Return(nil)
	aiHelper.
		On("GetEndpoint", *mockContext.Context, scopeType, endpointName).
		Return(onlineEndpoint, nil)

	serviceTarget := createMlEndpointTarget(mockContext, env, aiHelper)
	deployResult, err := logProgress(t, func(progess *async.Progress[ServiceProgress]) (*ServiceDeployResult, error) {
		serviceContext := NewServiceContext()
		serviceContext.Package = ArtifactCollection{
			{
				Kind:         ArtifactKindContainer,
				Location:     "test-flow-package",
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"name": "test-package",
					"type": "flow",
				},
			},
		}
		return serviceTarget.Deploy(*mockContext.Context, serviceConfig, serviceContext, targetResource, progess)

	})

	require.NoError(t, err)
	require.NotNil(t, deployResult)
	require.GreaterOrEqual(t, len(deployResult.Artifacts), 1)

	// Check that we have the expected deployment artifacts
	deploymentArtifacts := deployResult.Artifacts.Find(WithKind(ArtifactKindDeployment))
	require.Len(
		t,
		deploymentArtifacts,
		4,
		"Should have exactly 4 deployment artifacts: flow, environment, model, and online deployment",
	)

	// Verify specific deployment artifacts are present
	var flowArtifact, environmentArtifact, modelArtifact, onlineDeploymentArtifact *Artifact
	for _, artifact := range deploymentArtifacts {
		if artifact.LocationKind == LocationKindLocal && artifact.Location == "./flow/flow.yaml" {
			flowArtifact = artifact
		} else if artifact.LocationKind == LocationKindRemote {
			if artifact.Metadata["type"] == "Microsoft.MachineLearningServices/workspaces/environments/versions" {
				environmentArtifact = artifact
			} else if artifact.Metadata["type"] == "Microsoft.MachineLearningServices/workspaces/models/versions" {
				modelArtifact = artifact
			} else if artifact.Metadata["type"] ==
				"Microsoft.MachineLearningServices/workspaces/onlineEndpoints/deployments" {
				onlineDeploymentArtifact = artifact
			}
		}
	}

	// Assert that all expected deployment artifacts are present
	require.NotNil(t, flowArtifact, "Flow deployment artifact should be present")
	require.Equal(t, "./flow/flow.yaml", flowArtifact.Location, "Flow location should be correct")
	require.NotEmpty(t, flowArtifact.Metadata["name"], "Flow artifact should have a name")
	require.NotNil(t, environmentArtifact, "Environment version deployment artifact should be present")
	require.Equal(t, "1", environmentArtifact.Metadata["name"], "Environment version name should match")
	require.Contains(
		t,
		environmentArtifact.Location,
		"/environments/test/versions/1",
		"Environment version location should be correct",
	)

	require.NotNil(t, modelArtifact, "Model version deployment artifact should be present")
	require.Equal(t, "1", modelArtifact.Metadata["name"], "Model version name should match")
	require.Contains(t, modelArtifact.Location, "/models/test/versions/1", "Model version location should be correct")

	require.NotNil(t, onlineDeploymentArtifact, "Online deployment artifact should be present")
	require.Equal(
		t,
		expectedDeploymentName,
		onlineDeploymentArtifact.Metadata["name"],
		"Online deployment name should match expected deployment name",
	)
	require.Contains(
		t,
		onlineDeploymentArtifact.Location,
		"/deployments/"+expectedDeploymentName,
		"Online deployment location should contain deployment name",
	)

	// Check that we have endpoint artifacts
	endpoints := deployResult.Artifacts.Find()
	require.GreaterOrEqual(t, len(endpoints), 1)
}

func createMlEndpointTarget(
	mockContext *mocks.MockContext,
	env *environment.Environment,
	aiHelper AiHelper,
) ServiceTarget {
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", *mockContext.Context, env).Return(nil)

	return NewAiEndpointTarget(env, envManager, aiHelper)
}

type mockAiHelper struct {
	mock.Mock
}

func (m *mockAiHelper) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	args := m.Called(ctx)
	return args.Get(0).([]tools.ExternalTool)
}

func (m *mockAiHelper) Initialize(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *mockAiHelper) ValidateWorkspace(ctx context.Context, scope *ai.Scope) error {
	args := m.Called(ctx, scope)
	return args.Error(0)
}

func (m *mockAiHelper) CreateEnvironmentVersion(
	ctx context.Context,
	scope *ai.Scope,
	serviceConfig *ServiceConfig,
	config *ai.ComponentConfig,
) (*armmachinelearning.EnvironmentVersion, error) {
	args := m.Called(ctx, scope, serviceConfig, config)
	return args.Get(0).(*armmachinelearning.EnvironmentVersion), args.Error(1)
}

func (m *mockAiHelper) CreateModelVersion(
	ctx context.Context,
	scope *ai.Scope,
	serviceConfig *ServiceConfig,
	config *ai.ComponentConfig,
) (*armmachinelearning.ModelVersion, error) {
	args := m.Called(ctx, scope, serviceConfig, config)
	return args.Get(0).(*armmachinelearning.ModelVersion), args.Error(1)
}

func (m *mockAiHelper) GetEndpoint(
	ctx context.Context,
	scope *ai.Scope,
	endpointName string,
) (*armmachinelearning.OnlineEndpoint, error) {
	args := m.Called(ctx, scope, endpointName)
	return args.Get(0).(*armmachinelearning.OnlineEndpoint), args.Error(1)
}

func (m *mockAiHelper) DeployToEndpoint(
	ctx context.Context,
	scope *ai.Scope,
	serviceConfig *ServiceConfig,
	endpointName string,
	config *ai.EndpointDeploymentConfig,
) (*armmachinelearning.OnlineDeployment, error) {
	args := m.Called(ctx, scope, serviceConfig, endpointName, config)
	return args.Get(0).(*armmachinelearning.OnlineDeployment), args.Error(1)
}

func (m *mockAiHelper) CreateFlow(
	ctx context.Context,
	scope *ai.Scope,
	serviceConfig *ServiceConfig,
	config *ai.ComponentConfig,
) (*ai.Flow, error) {
	args := m.Called(ctx, scope, serviceConfig, config)
	return args.Get(0).(*ai.Flow), args.Error(1)
}

func (m *mockAiHelper) DeleteDeployments(ctx context.Context, scope *ai.Scope, endpointName string, filter []string) error {
	args := m.Called(ctx, scope, endpointName)
	return args.Error(0)
}

func (m *mockAiHelper) UpdateTraffic(
	ctx context.Context,
	scope *ai.Scope,
	endpointName string,
	deploymentName string,
) (*armmachinelearning.OnlineEndpoint, error) {
	args := m.Called(ctx, scope, endpointName, deploymentName)
	return args.Get(0).(*armmachinelearning.OnlineEndpoint), args.Error(1)
}

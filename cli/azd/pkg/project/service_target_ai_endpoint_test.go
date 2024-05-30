package project

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
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

	servicePackage := &ServicePackageResult{}
	targetResource := environment.NewTargetResource(
		env.GetSubscriptionId(),
		env.Getenv(environment.ResourceGroupEnvVarName),
		endpointName,
		string(infra.AzureMachineLearningEndpoint),
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
	}

	environmentVersion := &armmachinelearning.EnvironmentVersion{
		Name: convert.RefOf("1"),
	}

	modelVersion := &armmachinelearning.ModelVersion{
		Name: convert.RefOf("1"),
	}

	onlineDeployment := &armmachinelearning.OnlineDeployment{
		Name: &expectedDeploymentName,
	}

	onlineEndpoint := &armmachinelearning.OnlineEndpoint{
		Name: convert.RefOf(endpointName),
		Properties: &armmachinelearning.OnlineEndpointProperties{
			ScoringURI: convert.RefOf("https://SCRORING_URI"),
			SwaggerURI: convert.RefOf("https://SWAGGER_URI"),
			Traffic: map[string]*int32{
				deploymentName: convert.RefOf(int32(100)),
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
	deployTask := serviceTarget.Deploy(*mockContext.Context, serviceConfig, servicePackage, targetResource)
	require.NotNil(t, deployTask)
	logProgress(deployTask)

	deployResult, err := deployTask.Await()

	require.NoError(t, err)
	require.NotNil(t, deployResult)
	require.IsType(t, &AiEndpointDeploymentResult{}, deployResult.Details)
	require.Len(t, deployResult.Endpoints, 4)

	deploymentDetails := deployResult.Details.(*AiEndpointDeploymentResult)

	require.Equal(t, expectedFlowName, deploymentDetails.Flow.DisplayName)
	require.Equal(t, environmentVersion.Name, deploymentDetails.Environment.Name)
	require.Equal(t, modelVersion.Name, deploymentDetails.Model.Name)
	require.Equal(t, expectedDeploymentName, *deploymentDetails.Deployment.Name)
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

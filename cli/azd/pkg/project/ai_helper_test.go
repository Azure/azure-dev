package project

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockai"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_AiHelper_Init(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	env := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})
	mockPythonBridge := &mockPythonBridge{}
	aiHelper := newAiHelper(t, mockContext, env, mockPythonBridge)
	err := aiHelper.Initialize(*mockContext.Context)

	require.NoError(t, err)
	mockPythonBridge.AssertCalled(t, "Initialize", *mockContext.Context)
}

func Test_AiHelper_ValidateWorkspace(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	env := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
		environment.ResourceGroupEnvVarName:  "RESOURCE_GROUP",
	})
	scope := ai.NewScope(env.GetSubscriptionId(), env.Getenv(environment.ResourceGroupEnvVarName), "AI_WORKSPACE")
	mockPythonBridge := &mockPythonBridge{}
	mockai.RegisterGetWorkspaceMock(mockContext, scope.Workspace())

	aiHelper := newAiHelper(t, mockContext, env, mockPythonBridge)
	err := aiHelper.ValidateWorkspace(*mockContext.Context, scope)

	require.NoError(t, err)
}

func Test_AiHelper_CreateEnvironmentVersion(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	testDir := t.TempDir()
	env := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
		environment.ResourceGroupEnvVarName:  "RESOURCE_GROUP",
	})
	scope := ai.NewScope(env.GetSubscriptionId(), env.Getenv(environment.ResourceGroupEnvVarName), "AI_WORKSPACE")
	environmentName := "MY-ENVIRONMENT"

	emptyResult := exec.NewRunResult(0, "", "")

	mockPythonBridge := &mockPythonBridge{}
	mockPythonBridge.
		On("Run", *mockContext.Context, ai.MLClient, mock.Anything).
		Return(&emptyResult, nil)

	mockai.RegisterGetEnvironment(mockContext, scope.Workspace(), environmentName, http.StatusNotFound)
	mockai.RegisterGetEnvironmentVersion(mockContext, scope.Workspace(), environmentName, "1")

	serviceConfig := createTestServiceConfig("./contoso-chat", AiEndpointTarget, ServiceLanguagePython)
	serviceConfig.Project.Path = testDir
	environmentConfig := &ai.ComponentConfig{
		Name: osutil.NewExpandableString(environmentName),
		Path: "./deployment/environment.yaml",
	}

	createTestFile(t, testDir, filepath.Join(serviceConfig.RelativePath, environmentConfig.Path))

	aiHelper := newAiHelper(t, mockContext, env, mockPythonBridge)
	envVersion, err := aiHelper.CreateEnvironmentVersion(*mockContext.Context, scope, serviceConfig, environmentConfig)

	require.NoError(t, err)
	require.NotNil(t, envVersion)
	require.Equal(t, "1", *envVersion.Name)
	require.Equal(t, environmentName, env.Getenv(AiEnvironmentEnvVarName))

	mockPythonBridge.AssertCalled(t, "Run", *mockContext.Context, ai.MLClient, []string{
		"-t", "environment",
		"-s", scope.SubscriptionId(),
		"-g", scope.ResourceGroup(),
		"-w", scope.Workspace(),
		"-f", filepath.Join(serviceConfig.Path(), environmentConfig.Path),
		"--set", fmt.Sprintf("name=%s", environmentName),
		"--set", fmt.Sprintf("version=%d", 1),
	})
}

func Test_AiHelper_CreateModelVersion(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	testDir := t.TempDir()
	env := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
		environment.ResourceGroupEnvVarName:  "RESOURCE_GROUP",
	})
	scope := ai.NewScope(env.GetSubscriptionId(), env.Getenv(environment.ResourceGroupEnvVarName), "AI_WORKSPACE")
	modelName := "MY-MODEL"

	emptyResult := exec.NewRunResult(0, "", "")

	mockPythonBridge := &mockPythonBridge{}
	mockPythonBridge.
		On("Run", *mockContext.Context, ai.MLClient, mock.Anything).
		Return(&emptyResult, nil)

	mockai.RegisterGetModel(mockContext, scope.Workspace(), modelName, http.StatusNotFound)
	mockai.RegisterGetModelVersion(mockContext, scope.Workspace(), modelName, "1")

	serviceConfig := createTestServiceConfig("./contoso-chat", AiEndpointTarget, ServiceLanguagePython)
	serviceConfig.Project.Path = testDir
	modelConfig := &ai.ComponentConfig{
		Name: osutil.NewExpandableString(modelName),
		Path: "./deployment/model.yaml",
	}

	createTestFile(t, testDir, filepath.Join(serviceConfig.RelativePath, modelConfig.Path))

	aiHelper := newAiHelper(t, mockContext, env, mockPythonBridge)
	modelVersion, err := aiHelper.CreateModelVersion(*mockContext.Context, scope, serviceConfig, modelConfig)

	require.NoError(t, err)
	require.NotNil(t, modelVersion)
	require.Equal(t, "1", *modelVersion.Name)
	require.Equal(t, modelName, env.Getenv(AiModelEnvVarName))

	mockPythonBridge.AssertCalled(t, "Run", *mockContext.Context, ai.MLClient, []string{
		"-t", "model",
		"-s", scope.SubscriptionId(),
		"-g", scope.ResourceGroup(),
		"-w", scope.Workspace(),
		"-f", filepath.Join(serviceConfig.Path(), modelConfig.Path),
		"--set", fmt.Sprintf("name=%s", modelName),
		"--set", fmt.Sprintf("version=%d", 1),
	})
}

func Test_AiHelper_GetEndpoint(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	env := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
		environment.ResourceGroupEnvVarName:  "RESOURCE_GROUP",
	})
	scope := ai.NewScope(env.GetSubscriptionId(), env.Getenv(environment.ResourceGroupEnvVarName), "AI_WORKSPACE")
	endpointName := "MY-ENDPOINT"

	mockPythonBridge := &mockPythonBridge{}
	mockai.RegisterGetOnlineEndpoint(mockContext, scope.Workspace(), endpointName, http.StatusOK, nil)

	aiHelper := newAiHelper(t, mockContext, env, mockPythonBridge)
	endpoint, err := aiHelper.GetEndpoint(*mockContext.Context, scope, endpointName)

	require.NoError(t, err)
	require.NotNil(t, endpoint)
	require.Equal(t, endpointName, *endpoint.Name)
}

func Test_AiHelper_CreateFlow(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	testDir := t.TempDir()
	env := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
		environment.ResourceGroupEnvVarName:  "RESOURCE_GROUP",
	})
	scope := ai.NewScope(env.GetSubscriptionId(), env.Getenv(environment.ResourceGroupEnvVarName), "AI_WORKSPACE")
	flowName := "MY-FLOW"

	mockContext.Clock.Set(time.Now())
	expectedFlowName := fmt.Sprintf("%s-%d", flowName, mockContext.Clock.Now().Unix())
	expectedFlow := ai.Flow{
		Name:        uuid.New().String(),
		DisplayName: expectedFlowName,
		Type:        "chat",
	}

	jsonBytes, err := json.Marshal(expectedFlow)
	require.NoError(t, err)
	expectedFlowJson := string(jsonBytes)

	mockPythonBridge := &mockPythonBridge{}
	mockPythonBridge.
		On("Run", *mockContext.Context, ai.PromptFlowClient, mock.Anything).
		Return(&exec.RunResult{
			ExitCode: 0,
			Stdout:   expectedFlowJson,
			Stderr:   "",
		}, nil)

	serviceConfig := createTestServiceConfig("./contoso-chat", AiEndpointTarget, ServiceLanguagePython)
	serviceConfig.Project.Path = testDir
	flowConfig := &ai.ComponentConfig{
		Name: osutil.NewExpandableString(flowName),
		Path: "./my-flow",
	}

	createTestFile(t, testDir, filepath.Join(serviceConfig.RelativePath, filepath.Join(flowConfig.Path, "flow.dag.yaml")))

	aiHelper := newAiHelper(t, mockContext, env, mockPythonBridge)
	flow, err := aiHelper.CreateFlow(*mockContext.Context, scope, serviceConfig, flowConfig)

	require.NoError(t, err)
	require.NotNil(t, flow)
	require.True(t, strings.HasPrefix(flow.DisplayName, flowName))
	require.Equal(t, expectedFlowName, flow.DisplayName)
	require.Equal(t, expectedFlowName, env.Getenv(AiFlowEnvVarName), flowName)
	mockPythonBridge.AssertCalled(t, "Run", *mockContext.Context, ai.PromptFlowClient, []string{
		"create",
		"-n", expectedFlowName,
		"-f", filepath.Join(serviceConfig.Path(), flowConfig.Path),
		"-s", scope.SubscriptionId(),
		"-g", scope.ResourceGroup(),
		"-w", scope.Workspace(),
	})
}

func Test_AiHelper_DeployToEndpoint(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	testDir := t.TempDir()
	env := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
		environment.ResourceGroupEnvVarName:  "RESOURCE_GROUP",
		"MY_VAR":                             "MY_VALUE",
	})
	scope := ai.NewScope(env.GetSubscriptionId(), env.Getenv(environment.ResourceGroupEnvVarName), "AI_WORKSPACE")
	environmentName := "MY-ENVIRONMENT"
	environmentVersion := "2"
	modelName := "MY-MODEL"
	modelVersion := "2"
	endpointName := "MY-ENDPOINT"
	deploymentName := "MY-DEPLOYMENT"

	mockContext.Clock.Set(time.Now())
	expectedDeploymentName := fmt.Sprintf("%s-%d", deploymentName, mockContext.Clock.Now().Unix())

	mockPythonBridge := &mockPythonBridge{}
	mockPythonBridge.
		On("Run", *mockContext.Context, ai.MLClient, mock.Anything).
		Return(&exec.RunResult{}, nil)

	mockai.RegisterGetEnvironment(mockContext, scope.Workspace(), environmentName, http.StatusOK)
	mockai.RegisterGetEnvironmentVersion(mockContext, scope.Workspace(), environmentName, environmentVersion)
	mockai.RegisterGetModel(mockContext, scope.Workspace(), modelName, http.StatusOK)
	mockai.RegisterGetModelVersion(mockContext, scope.Workspace(), modelName, modelVersion)
	mockai.RegisterGetOnlineDeployment(
		mockContext,
		scope.Workspace(),
		endpointName,
		expectedDeploymentName,
		armmachinelearning.DeploymentProvisioningStateSucceeded,
	)

	serviceConfig := createTestServiceConfig("./contoso-chat", AiEndpointTarget, ServiceLanguagePython)
	serviceConfig.Project.Path = testDir

	endpointConfig := &ai.EndpointDeploymentConfig{
		Workspace: osutil.NewExpandableString(scope.Workspace()),
		Flow: &ai.ComponentConfig{
			Path: "./my-flow",
		},
		Environment: &ai.ComponentConfig{
			Name: osutil.NewExpandableString(environmentName),
			Path: "./deployment/environment.yaml",
		},
		Model: &ai.ComponentConfig{
			Name: osutil.NewExpandableString(modelName),
			Path: "./deployment/model.yaml",
		},
		Deployment: &ai.DeploymentConfig{
			ComponentConfig: ai.ComponentConfig{
				Name: osutil.NewExpandableString(deploymentName),
				Path: "./deployment/deployment.yaml",
			},
			Environment: map[string]osutil.ExpandableString{
				"MY_VAR": osutil.NewExpandableString("${MY_VAR}"),
			},
		},
	}

	createTestFile(t, testDir, filepath.Join(serviceConfig.RelativePath, endpointConfig.Deployment.Path))

	aiHelper := newAiHelper(t, mockContext, env, mockPythonBridge)
	deployment, err := aiHelper.DeployToEndpoint(*mockContext.Context, scope, serviceConfig, endpointName, endpointConfig)

	require.NoError(t, err)
	require.NotNil(t, deployment)
	require.Equal(t, expectedDeploymentName, *deployment.Name)
	require.Equal(t, expectedDeploymentName, env.Getenv(AiDeploymentEnvVarName), *deployment.Name)
	require.Equal(t, endpointName, env.Getenv(AiEndpointEnvVarName))
	require.Equal(t, expectedDeploymentName, env.Getenv(AiDeploymentEnvVarName))
	mockPythonBridge.AssertCalled(t, "Run", *mockContext.Context, ai.MLClient, []string{
		"-t", "online-deployment",
		"-s", scope.SubscriptionId(),
		"-g", scope.ResourceGroup(),
		"-w", scope.Workspace(),
		"-f", filepath.Join(serviceConfig.Path(), endpointConfig.Deployment.Path),
		"--set", fmt.Sprintf("name=%s", expectedDeploymentName),
		"--set", fmt.Sprintf("endpoint_name=%s", endpointName),
		"--set", fmt.Sprintf("environment=azureml:%s:%s", environmentName, environmentVersion),
		"--set", fmt.Sprintf("model=azureml:%s:%s", modelName, modelVersion),
		"--set", fmt.Sprintf("environment_variables.MY_VAR=%s", env.Getenv("MY_VAR")),
	})
}

func Test_AiHelper_UpdateTraffic(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	env := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
		environment.ResourceGroupEnvVarName:  "RESOURCE_GROUP",
	})
	scope := ai.NewScope(env.GetSubscriptionId(), env.Getenv(environment.ResourceGroupEnvVarName), "AI_WORKSPACE")
	endpointName := "MY-ENDPOINT"
	deploymentName := "MY-DEPLOYMENT"

	mockContext.Clock.Set(time.Now())

	trafficMap := map[string]*int32{
		deploymentName: convert.RefOf(int32(100)),
	}
	endpoint := &armmachinelearning.OnlineEndpoint{
		Name: convert.RefOf(endpointName),
		Properties: &armmachinelearning.OnlineEndpointProperties{
			Traffic: map[string]*int32{
				deploymentName: convert.RefOf(int32(100)),
			},
		},
	}

	mockPythonBridge := &mockPythonBridge{}
	mockai.RegisterGetOnlineEndpoint(mockContext, scope.Workspace(), endpointName, http.StatusOK, endpoint)
	updateRequest := mockai.RegisterUpdateOnlineEndpoint(mockContext, scope.Workspace(), endpointName, trafficMap)

	aiHelper := newAiHelper(t, mockContext, env, mockPythonBridge)
	endpoint, err := aiHelper.UpdateTraffic(*mockContext.Context, scope, endpointName, deploymentName)

	require.NoError(t, err)
	require.NotNil(t, endpoint)
	require.NotNil(t, updateRequest)
	require.Equal(t, endpointName, *endpoint.Name)
	require.Equal(t, int32(100), *endpoint.Properties.Traffic[deploymentName])
}

func Test_AiHelper_DeletePreviousDeployments(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	env := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
		environment.ResourceGroupEnvVarName:  "RESOURCE_GROUP",
	})
	scope := ai.NewScope(env.GetSubscriptionId(), env.Getenv(environment.ResourceGroupEnvVarName), "AI_WORKSPACE")
	endpointName := "MY-ENDPOINT"
	deploymentName := "MY-DEPLOYMENT"

	endpoint := &armmachinelearning.OnlineEndpoint{
		Name: convert.RefOf(endpointName),
		Properties: &armmachinelearning.OnlineEndpointProperties{
			Traffic: map[string]*int32{
				deploymentName: convert.RefOf(int32(100)),
			},
		},
	}

	existingDeploymentNames := []string{
		"old-deploymen-01",
		"old-deployment-02",
	}

	mockPythonBridge := &mockPythonBridge{}
	mockai.RegisterGetOnlineEndpoint(mockContext, scope.Workspace(), endpointName, http.StatusOK, endpoint)
	mockai.RegisterListOnlineDeployment(mockContext, scope.Workspace(), endpointName, existingDeploymentNames)

	deleteRequests := []*http.Request{}

	for _, deploymentName := range existingDeploymentNames {
		deleteRequests = append(deleteRequests, mockai.RegisterDeleteOnlineDeployment(
			mockContext,
			scope.Workspace(),
			endpointName,
			deploymentName,
		))
	}

	aiHelper := newAiHelper(t, mockContext, env, mockPythonBridge)
	err := aiHelper.DeleteDeployments(*mockContext.Context, scope, endpointName, []string{"MY-DEPLOYMENT"})
	require.Len(t, deleteRequests, len(existingDeploymentNames))

	require.NoError(t, err)
}

func createTestFile(t *testing.T, tempDir string, relativePath string) {
	destPath := filepath.Join(tempDir, relativePath)
	err := os.MkdirAll(filepath.Dir(destPath), osutil.PermissionDirectory)
	require.NoError(t, err)

	err = os.WriteFile(destPath, []byte(""), osutil.PermissionFile)
	require.NoError(t, err)
}

func newAiHelper(
	t *testing.T,
	mockContext *mocks.MockContext,
	env *environment.Environment,
	mockPythonBridge *mockPythonBridge,
) AiHelper {
	aiHelper := NewAiHelper(
		env,
		mockContext.Clock,
		mockPythonBridge,
		mockContext.SubscriptionCredentialProvider,
		mockContext.ArmClientOptions,
	)

	mockPythonBridge.On("Initialize", *mockContext.Context).Return(nil)

	err := aiHelper.Initialize(*mockContext.Context)
	require.NoError(t, err)

	return aiHelper
}

type mockPythonBridge struct {
	mock.Mock
}

func (m *mockPythonBridge) Initialize(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *mockPythonBridge) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	args := m.Called(ctx)
	return args.Get(0).([]tools.ExternalTool)
}

func (m *mockPythonBridge) Run(ctx context.Context, scriptName ai.ScriptPath, args ...string) (*exec.RunResult, error) {
	callArgs := m.Called(ctx, scriptName, args)
	return callArgs.Get(0).(*exec.RunResult), callArgs.Error(1)
}

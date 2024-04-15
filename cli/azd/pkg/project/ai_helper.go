package project

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

type AiHelper struct {
	azdCtx             *azdcontext.AzdContext
	env                *environment.Environment
	credentialProvider account.SubscriptionCredentialProvider
	armClientOptions   *arm.ClientOptions
	commandRunner      exec.CommandRunner
	pythonBridge       *ai.PythonBridge
	credentials        azcore.TokenCredential
	initialized        bool
}

func NewAiHelper(
	azdCtx *azdcontext.AzdContext,
	env *environment.Environment,
	armClientOptions *arm.ClientOptions,
	credentialProvider account.SubscriptionCredentialProvider,
	commandRunner exec.CommandRunner,
	pythonBridge *ai.PythonBridge,
) *AiHelper {
	return &AiHelper{
		azdCtx:             azdCtx,
		env:                env,
		armClientOptions:   armClientOptions,
		credentialProvider: credentialProvider,
		commandRunner:      commandRunner,
		pythonBridge:       pythonBridge,
	}
}

func (a *AiHelper) Init(ctx context.Context) error {
	if a.initialized {
		return nil
	}

	credentials, err := a.credentialProvider.CredentialForSubscription(ctx, a.env.GetSubscriptionId())
	if err != nil {
		return err
	}

	if err := a.pythonBridge.Initialize(ctx); err != nil {
		return err
	}

	a.credentials = credentials
	a.initialized = true
	return nil
}

func (a *AiHelper) EnsureWorkspace(
	ctx context.Context,
	scope *ai.Scope,
) error {
	workspaceClient, err := armmachinelearning.NewWorkspacesClient(
		scope.SubscriptionId(),
		a.credentials,
		a.armClientOptions,
	)
	if err != nil {
		return err
	}

	workspaceName := scope.Workspace()

	workspaceResponse, err := workspaceClient.Get(
		ctx,
		scope.ResourceGroup(),
		workspaceName,
		nil,
	)
	if err != nil {
		return err
	}

	if *workspaceResponse.Workspace.Name != workspaceName {
		return err
	}

	return nil
}

func (a *AiHelper) CreateEnvironmentVersion(
	ctx context.Context,
	scope *ai.Scope,
	serviceConfig *ServiceConfig,
	config *ai.ComponentConfig,
) (*armmachinelearning.EnvironmentVersion, error) {
	yamlFilePath := filepath.Join(serviceConfig.Path(), config.Path)
	_, err := os.Stat(yamlFilePath)
	if err != nil {
		return nil, err
	}

	environmentsClient, err := armmachinelearning.NewEnvironmentContainersClient(
		scope.SubscriptionId(),
		a.credentials,
		a.armClientOptions,
	)
	if err != nil {
		return nil, err
	}

	environmentName, err := config.Name.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing environment name value: %w", err)
	}

	if environmentName == "" {
		environmentName = fmt.Sprintf("%s-environment", serviceConfig.Name)
	}

	nextVersion := "1"
	envContainerResponse, err := environmentsClient.Get(
		ctx,
		scope.ResourceGroup(),
		scope.Workspace(),
		environmentName,
		nil,
	)
	if err == nil {
		nextVersion = *envContainerResponse.Properties.NextVersion
	}

	environmentArgs := []string{
		"-t", "environment",
		"-s", scope.SubscriptionId(),
		"-g", scope.ResourceGroup(),
		"-w", scope.Workspace(),
		"-f", yamlFilePath,
		"--set", fmt.Sprintf("name=%s", environmentName),
		"--set", fmt.Sprintf("version=%s", nextVersion),
	}

	environmentArgs, err = a.applyOverrides(environmentArgs, config.Overrides)
	if err != nil {
		return nil, err
	}

	if _, err := a.pythonBridge.Run(ctx, ai.MLClient, environmentArgs...); err != nil {
		return nil, err
	}

	envVersionsClient, err := armmachinelearning.NewEnvironmentVersionsClient(
		scope.SubscriptionId(),
		a.credentials,
		a.armClientOptions,
	)
	if err != nil {
		return nil, err
	}

	envVersionResponse, err := envVersionsClient.Get(
		ctx,
		scope.ResourceGroup(),
		scope.Workspace(),
		environmentName,
		nextVersion,
		nil,
	)
	if err != nil {
		return nil, err
	}

	a.env.DotenvSet("AZUREML_ENVIRONMENT_NAME", environmentName)

	return &envVersionResponse.EnvironmentVersion, nil
}

func (a *AiHelper) CreateModelVersion(
	ctx context.Context,
	scope *ai.Scope,
	serviceConfig *ServiceConfig,
	config *ai.ComponentConfig,
) (*armmachinelearning.ModelVersion, error) {
	yamlFilePath := filepath.Join(serviceConfig.Path(), config.Path)
	_, err := os.Stat(yamlFilePath)
	if err != nil {
		return nil, err
	}

	modelName, err := config.Name.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing model name value: %w", err)
	}

	if modelName == "" {
		modelName = fmt.Sprintf("%s-model", serviceConfig.Name)
	}

	modelArgs := []string{
		"-t", "model",
		"-s", scope.SubscriptionId(),
		"-g", scope.ResourceGroup(),
		"-w", scope.Workspace(),
		"-f", yamlFilePath,
		"--set", fmt.Sprintf("name=%s", modelName),
	}

	modelArgs, err = a.applyOverrides(modelArgs, config.Overrides)
	if err != nil {
		return nil, err
	}

	if _, err := a.pythonBridge.Run(ctx, ai.MLClient, modelArgs...); err != nil {
		return nil, err
	}

	modelContainerClient, err := armmachinelearning.NewModelContainersClient(
		scope.SubscriptionId(),
		a.credentials,
		a.armClientOptions,
	)
	if err != nil {
		return nil, err
	}

	modelContainerResponse, err := modelContainerClient.Get(
		ctx,
		scope.ResourceGroup(),
		scope.Workspace(),
		modelName,
		nil,
	)
	if err != nil {
		return nil, err
	}

	modelContainer := &modelContainerResponse.ModelContainer

	modelVersionClient, err := armmachinelearning.NewModelVersionsClient(
		scope.SubscriptionId(),
		a.credentials,
		a.armClientOptions,
	)
	if err != nil {
		return nil, err
	}

	latestVersion := "1"
	if modelContainer.Properties.LatestVersion != nil {
		latestVersion = *modelContainer.Properties.LatestVersion
	}

	modelVersionResponse, err := modelVersionClient.Get(
		ctx,
		scope.ResourceGroup(),
		scope.Workspace(),
		modelName,
		latestVersion,
		nil,
	)
	if err != nil {
		return nil, err
	}

	a.env.DotenvSet("AZUREML_MODEL_NAME", modelName)

	return &modelVersionResponse.ModelVersion, nil
}

func (a *AiHelper) GetEndpoint(
	ctx context.Context,
	scope *ai.Scope,
	endpointName string,
) (*armmachinelearning.OnlineEndpoint, error) {
	endpointClient, err := armmachinelearning.NewOnlineEndpointsClient(
		scope.SubscriptionId(),
		a.credentials,
		a.armClientOptions,
	)
	if err != nil {
		return nil, err
	}

	endpointResponse, err := endpointClient.Get(
		ctx,
		scope.ResourceGroup(),
		scope.Workspace(),
		endpointName,
		nil,
	)
	if err != nil {
		return nil, err
	}

	return &endpointResponse.OnlineEndpoint, nil
}

func (a *AiHelper) DeployToEndpoint(
	ctx context.Context,
	scope *ai.Scope,
	serviceConfig *ServiceConfig,
	endpointName string,
	config *ai.EndpointDeploymentConfig,
) (*armmachinelearning.OnlineDeployment, error) {
	// Get the custom environment name if configured
	environmentVersionName, err := a.getEnvironmentVersionName(ctx, scope, serviceConfig, config)
	if err != nil {
		return nil, err
	}

	// Get the custom model name if configured
	modelVersionName, err := a.getModelVersionName(ctx, scope, serviceConfig, config)
	if err != nil {
		return nil, err
	}

	deploymentName := fmt.Sprintf("azd-%d", time.Now().Unix())

	yamlFilePath := filepath.Join(serviceConfig.Path(), config.Deployment.Path)
	_, err = os.Stat(yamlFilePath)
	if err != nil {
		return nil, err
	}

	a.env.DotenvSet("AZUREML_ENDPOINT_NAME", endpointName)
	a.env.DotenvSet("AZUREML_DEPLOYMENT_NAME", deploymentName)

	deploymentArgs := []string{
		"-t", "online-deployment",
		"-s", scope.SubscriptionId(),
		"-g", scope.ResourceGroup(),
		"-w", scope.Workspace(),
		"-f", yamlFilePath,
		"--set", fmt.Sprintf("name=%s", deploymentName),
		"--set", fmt.Sprintf("endpoint_name=%s", endpointName),
	}

	if environmentVersionName != "" {
		deploymentArgs = append(deploymentArgs, "--set", fmt.Sprintf("environment=%s", environmentVersionName))
	}

	if modelVersionName != "" {
		deploymentArgs = append(deploymentArgs, "--set", fmt.Sprintf("model=%s", modelVersionName))
	}

	deploymentArgs, err = a.applyOverrides(deploymentArgs, config.Deployment.Overrides)
	if err != nil {
		return nil, err
	}

	_, err = a.pythonBridge.Run(ctx, ai.MLClient, deploymentArgs...)
	if err != nil {
		return nil, err
	}

	deploymentsClient, err := armmachinelearning.NewOnlineDeploymentsClient(
		scope.SubscriptionId(),
		a.credentials,
		a.armClientOptions,
	)
	if err != nil {
		return nil, err
	}

	deploymentResponse, err := deploymentsClient.Get(
		ctx,
		scope.ResourceGroup(),
		scope.Workspace(),
		endpointName,
		deploymentName,
		nil,
	)
	if err != nil {
		return nil, err
	}

	return &deploymentResponse.OnlineDeployment, nil
}

func (a *AiHelper) CreateOrUpdateFlow(
	ctx context.Context,
	scope *ai.Scope,
	serviceConfig *ServiceConfig,
	config *ai.ComponentConfig,
) (*ai.Flow, error) {
	flowName, err := config.Name.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing flow name value: %w", err)
	}

	if flowName == "" {
		flowName = fmt.Sprintf("%s-flow", serviceConfig.Name)
	}

	flowPath := filepath.Join(serviceConfig.Path(), config.Path)
	_, err = os.Stat(flowPath)
	if err != nil {
		return nil, err
	}

	flowName = fmt.Sprintf("%s-%d", flowName, time.Now().Unix())

	getArgs := []string{
		"show",
		"-s", scope.SubscriptionId(),
		"-w", scope.Workspace(),
		"-g", scope.ResourceGroup(),
		"-n", flowName,
	}

	var createOrUpdateArgs []string
	_, err = a.pythonBridge.Run(ctx, ai.PromptFlowClient, getArgs...)
	if err == nil {
		createOrUpdateArgs = []string{"update", "-n", flowName}
	} else {
		createOrUpdateArgs = []string{"create", "-n", flowName, "-f", flowPath}
	}

	createOrUpdateArgs = append(createOrUpdateArgs,
		"-s", scope.SubscriptionId(),
		"-w", scope.Workspace(),
		"-g", scope.ResourceGroup(),
	)

	createOrUpdateArgs, err = a.applyOverrides(createOrUpdateArgs, config.Overrides)
	if err != nil {
		return nil, err
	}

	result, err := a.pythonBridge.Run(ctx, ai.PromptFlowClient, createOrUpdateArgs...)
	if err != nil {
		return nil, fmt.Errorf("flow operation failed: %w", err)
	}

	var existingFlow *ai.Flow
	err = json.Unmarshal([]byte(result.Stdout), &existingFlow)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal flow: %w", err)
	}

	a.env.DotenvSet("AZUREML_FLOW_NAME", flowName)

	return existingFlow, nil
}

func (a *AiHelper) getEnvironmentVersionName(
	ctx context.Context,
	scope *ai.Scope,
	serviceConfig *ServiceConfig,
	config *ai.EndpointDeploymentConfig,
) (string, error) {
	if config.Environment == nil {
		return "", nil
	}

	environmentName, err := config.Environment.Name.Envsubst(a.env.Getenv)
	if err != nil {
		return "", fmt.Errorf("failed parsing environment name value: %w", err)
	}

	if environmentName == "" {
		environmentName = fmt.Sprintf("%s-environment", serviceConfig.Name)
	}

	envClient, err := armmachinelearning.NewEnvironmentContainersClient(
		scope.SubscriptionId(),
		a.credentials,
		a.armClientOptions,
	)
	if err != nil {
		return "", err
	}

	envGetResponse, err := envClient.Get(
		ctx,
		scope.ResourceGroup(),
		scope.Workspace(),
		environmentName,
		nil,
	)
	if err != nil {
		return "", err
	}

	environmentContainer := envGetResponse.EnvironmentContainer
	return fmt.Sprintf(
		"azureml:%s:%s",
		*environmentContainer.Name,
		*environmentContainer.Properties.LatestVersion,
	), nil
}

func (a *AiHelper) getModelVersionName(
	ctx context.Context,
	scope *ai.Scope,
	serviceConfig *ServiceConfig,
	config *ai.EndpointDeploymentConfig,
) (string, error) {
	if config.Model == nil {
		return "", nil
	}

	modelName, err := config.Model.Name.Envsubst(a.env.Getenv)
	if err != nil {
		return "", fmt.Errorf("failed parsing model name value: %w", err)
	}

	if modelName == "" {
		modelName = fmt.Sprintf("%s-model", serviceConfig.Name)
	}

	modelClient, err := armmachinelearning.NewModelContainersClient(
		scope.SubscriptionId(),
		a.credentials,
		a.armClientOptions,
	)
	if err != nil {
		return "", err
	}

	modelGetResponse, err := modelClient.Get(
		ctx,
		scope.ResourceGroup(),
		scope.Workspace(),
		modelName,
		nil,
	)
	if err != nil {
		return "", err
	}

	modelContainer := modelGetResponse.ModelContainer
	return fmt.Sprintf(
		"azureml:%s:%s",
		*modelContainer.Name,
		*modelContainer.Properties.LatestVersion,
	), nil
}

func (a *AiHelper) applyOverrides(args []string, overrides map[string]osutil.ExpandableString) ([]string, error) {
	for key, value := range overrides {
		expandedValue, err := value.Envsubst(a.env.Getenv)
		if err != nil {
			return nil, fmt.Errorf("failed parsing environment override %s: %w", key, err)
		}

		args = append(args, "--set", fmt.Sprintf("%s=%s", key, expandedValue))
	}

	return args, nil
}

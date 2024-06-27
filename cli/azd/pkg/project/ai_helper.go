package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/benbjohnson/clock"
	"github.com/sethvargo/go-retry"
)

const (
	// AiHubNameEnvVarName is the environment variable name for the Azure AI Hub name
	AiHubNameEnvVarName = "AZUREAI_HUB_NAME"
	// AiProjectNameEnvVarName is the environment variable name for the Azure AI project name
	AiProjectNameEnvVarName = "AZUREAI_PROJECT_NAME"
	/* #nosec G101 - Potential hardcoded credentials - false positive */
	// AiEnvironmentEnvVarName is the environment variable name for the Azure AI environment name
	AiEnvironmentEnvVarName = "AZUREAI_ENVIRONMENT_NAME"
	// AiModelEnvVarName is the environment variable name for the Azure AI model name
	AiModelEnvVarName = "AZUREAI_MODEL_NAME"
	// AiEndpointEnvVarName is the environment variable name for the Azure AI endpoint name
	AiEndpointEnvVarName = "AZUREAI_ENDPOINT_NAME"
	// AiDeploymentEnvVarName is the environment variable name for the Azure AI deployment name
	AiDeploymentEnvVarName = "AZUREAI_DEPLOYMENT_NAME"
	// AiFlowEnvVarName is the environment variable name for the Azure AI flow name
	AiFlowEnvVarName = "AZUREAI_FLOW_NAME"
)

type AiHelper interface {
	// RequiredExternalTools returns the required external tools for the AiHelper
	RequiredExternalTools(ctx context.Context) []tools.ExternalTool
	// Initialize initializes the AiHelper
	Initialize(ctx context.Context) error
	// ValidateWorkspace ensures that the workspace exists
	ValidateWorkspace(ctx context.Context, scope *ai.Scope) error
	// CreateEnvironmentVersion creates a new environment version
	CreateEnvironmentVersion(
		ctx context.Context,
		scope *ai.Scope,
		serviceConfig *ServiceConfig,
		config *ai.ComponentConfig,
	) (*armmachinelearning.EnvironmentVersion, error)
	// CreateModelVersion creates a new model version
	CreateModelVersion(
		ctx context.Context,
		scope *ai.Scope,
		serviceConfig *ServiceConfig,
		config *ai.ComponentConfig,
	) (*armmachinelearning.ModelVersion, error)
	// GetEndpoint retrieves an online endpoint
	GetEndpoint(ctx context.Context, scope *ai.Scope, endpointName string) (*armmachinelearning.OnlineEndpoint, error)
	// DeployToEndpoint deploys a new online deployment to an online endpoint
	DeployToEndpoint(
		ctx context.Context,
		scope *ai.Scope,
		serviceConfig *ServiceConfig,
		endpointName string,
		config *ai.EndpointDeploymentConfig,
	) (*armmachinelearning.OnlineDeployment, error)
	// DeleteDeployments deletes all deployments of an online endpoint except the ones in filter
	DeleteDeployments(ctx context.Context, scope *ai.Scope, endpointName string, filter []string) error
	// UpdateTraffic updates the traffic distribution of an online endpoint for the specified deployment
	UpdateTraffic(
		ctx context.Context,
		scope *ai.Scope,
		endpointName string,
		deploymentName string,
	) (*armmachinelearning.OnlineEndpoint, error)
	// CreateFlow creates a new flow
	CreateFlow(
		ctx context.Context,
		scope *ai.Scope,
		serviceConfig *ServiceConfig,
		config *ai.ComponentConfig,
	) (*ai.Flow, error)
}

// aiHelper provides helper functions for interacting with Azure Machine Learning resources
type aiHelper struct {
	env                   *environment.Environment
	clock                 clock.Clock
	pythonBridge          ai.PythonBridge
	credentialProvider    account.SubscriptionCredentialProvider
	armClientOptions      *arm.ClientOptions
	workspacesClient      *armmachinelearning.WorkspacesClient
	envContainersClient   *armmachinelearning.EnvironmentContainersClient
	envVersionsClient     *armmachinelearning.EnvironmentVersionsClient
	modelContainersClient *armmachinelearning.ModelContainersClient
	modelVersionsClient   *armmachinelearning.ModelVersionsClient
	endpointsClient       *armmachinelearning.OnlineEndpointsClient
	deploymentsClient     *armmachinelearning.OnlineDeploymentsClient
	initialized           bool
}

// NewAiHelper creates a new instance of AiHelper
func NewAiHelper(
	env *environment.Environment,
	clock clock.Clock,
	pythonBridge ai.PythonBridge,
	credentialProvider account.SubscriptionCredentialProvider,
	armClientOptions *arm.ClientOptions,
) AiHelper {
	return &aiHelper{
		env:                env,
		clock:              clock,
		pythonBridge:       pythonBridge,
		credentialProvider: credentialProvider,
		armClientOptions:   armClientOptions,
	}
}

// RequiredExternalTools returns the required external tools for the AiHelper
func (a *aiHelper) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	return a.pythonBridge.RequiredExternalTools(ctx)
}

// Initialize initializes the AiHelper
func (a *aiHelper) Initialize(ctx context.Context) error {
	if a.initialized {
		return nil
	}

	subscriptionId := a.env.GetSubscriptionId()
	if subscriptionId == "" {
		return errors.New("subscription id is not set")
	}

	credential, err := a.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return err
	}

	clientFactory, err := armmachinelearning.NewClientFactory(subscriptionId, credential, a.armClientOptions)
	if err != nil {
		return err
	}

	a.workspacesClient = clientFactory.NewWorkspacesClient()
	a.envContainersClient = clientFactory.NewEnvironmentContainersClient()
	a.envVersionsClient = clientFactory.NewEnvironmentVersionsClient()
	a.modelContainersClient = clientFactory.NewModelContainersClient()
	a.modelVersionsClient = clientFactory.NewModelVersionsClient()
	a.endpointsClient = clientFactory.NewOnlineEndpointsClient()
	a.deploymentsClient = clientFactory.NewOnlineDeploymentsClient()

	if err := a.pythonBridge.Initialize(ctx); err != nil {
		return err
	}

	a.initialized = true
	return nil
}

// ValidateWorkspace ensures that the workspace exists
func (a *aiHelper) ValidateWorkspace(
	ctx context.Context,
	scope *ai.Scope,
) error {
	workspaceName := scope.Workspace()

	_, err := a.workspacesClient.Get(
		ctx,
		scope.ResourceGroup(),
		workspaceName,
		nil,
	)
	if err != nil {
		return err
	}

	return nil
}

// CreateEnvironmentVersion creates a new environment version
func (a *aiHelper) CreateEnvironmentVersion(
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

	environmentName, err := config.Name.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing environment name value: %w", err)
	}

	if environmentName == "" {
		environmentName = fmt.Sprintf("%s-environment", serviceConfig.Name)
	}

	nextVersion := "1"
	envContainerResponse, err := a.envContainersClient.Get(
		ctx,
		scope.ResourceGroup(),
		scope.Workspace(),
		environmentName,
		nil,
	)
	if err != nil {
		// An http 404 error is expected if the environment container does not exist
		// Therefore we default to version 1
		var httpErr *azcore.ResponseError
		isHttpError := errors.As(err, &httpErr)
		if !isHttpError || (isHttpError && httpErr.StatusCode != http.StatusNotFound) {
			return nil, fmt.Errorf("failed getting environment container: %w", err)
		}
	} else {
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

	envVersionResponse, err := a.envVersionsClient.Get(
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

	a.env.DotenvSet(AiEnvironmentEnvVarName, environmentName)

	return &envVersionResponse.EnvironmentVersion, nil
}

// CreateModelVersion creates a new model version
func (a *aiHelper) CreateModelVersion(
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

	nextVersion := "1"
	modelContainerResponse, err := a.modelContainersClient.Get(
		ctx,
		scope.ResourceGroup(),
		scope.Workspace(),
		modelName,
		nil,
	)
	if err != nil {
		// An http 404 error is expected if the environment container does not exist
		// Therefore we default to version 1
		var httpErr *azcore.ResponseError
		isHttpError := errors.As(err, &httpErr)
		if !isHttpError || (isHttpError && httpErr.StatusCode != http.StatusNotFound) {
			return nil, fmt.Errorf("failed getting environment container: %w", err)
		}
	} else {
		nextVersion = *modelContainerResponse.Properties.NextVersion
	}

	modelArgs := []string{
		"-t", "model",
		"-s", scope.SubscriptionId(),
		"-g", scope.ResourceGroup(),
		"-w", scope.Workspace(),
		"-f", yamlFilePath,
		"--set", fmt.Sprintf("name=%s", modelName),
		"--set", fmt.Sprintf("version=%s", nextVersion),
	}

	modelArgs, err = a.applyOverrides(modelArgs, config.Overrides)
	if err != nil {
		return nil, err
	}

	if _, err := a.pythonBridge.Run(ctx, ai.MLClient, modelArgs...); err != nil {
		return nil, err
	}

	modelVersionResponse, err := a.modelVersionsClient.Get(
		ctx,
		scope.ResourceGroup(),
		scope.Workspace(),
		modelName,
		nextVersion,
		nil,
	)
	if err != nil {
		return nil, err
	}

	a.env.DotenvSet(AiModelEnvVarName, modelName)

	return &modelVersionResponse.ModelVersion, nil
}

// GetEndpoint retrieves an online endpoint
func (a *aiHelper) GetEndpoint(
	ctx context.Context,
	scope *ai.Scope,
	endpointName string,
) (*armmachinelearning.OnlineEndpoint, error) {
	endpointResponse, err := a.endpointsClient.Get(
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

// DeployToEndpoint deploys a new online deployment to an online endpoint
func (a *aiHelper) DeployToEndpoint(
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

	deploymentName, err := config.Deployment.Name.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing deployment name value: %w", err)
	}

	// Deployment naming rules
	// Begin with a letter
	// Be 3-32 characters in length
	const minDeploymentNameLength = 3
	const maxDeploymentNameLength = 32

	if deploymentName == "" {
		deploymentName = fmt.Sprintf("%s-deployment", serviceConfig.Name)
	}

	timestampSuffix := fmt.Sprintf("-%d", a.clock.Now().Unix())
	maxDeploymentNameWithTimestamp := maxDeploymentNameLength - len(timestampSuffix)

	// Timestamp is typically 10 digits long so we can use the remaining characters for the deployment name
	if len(deploymentName) > maxDeploymentNameWithTimestamp {
		deploymentName = deploymentName[:maxDeploymentNameWithTimestamp]
	}

	deploymentName = fmt.Sprintf("%s%s", deploymentName, timestampSuffix)

	if len(deploymentName) < minDeploymentNameLength || len(deploymentName) > maxDeploymentNameLength {
		return nil, fmt.Errorf(
			"deployment '%s' must be between 3 and 32 characters. Update the deployment name within the azure.yaml.",
			deploymentName,
		)
	}

	yamlFilePath := filepath.Join(serviceConfig.Path(), config.Deployment.Path)
	_, err = os.Stat(yamlFilePath)
	if err != nil {
		return nil, err
	}

	a.env.DotenvSet(AiEndpointEnvVarName, endpointName)
	a.env.DotenvSet(AiDeploymentEnvVarName, deploymentName)

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

	if config.Deployment.Overrides == nil {
		config.Deployment.Overrides = map[string]osutil.ExpandableString{}
	}

	// Transform any referenced environment variables into override expressions
	for key, value := range config.Deployment.Environment {
		envVarOverrideKey := fmt.Sprintf("environment_variables.%s", key)
		config.Deployment.Overrides[envVarOverrideKey] = value
	}

	deploymentArgs, err = a.applyOverrides(deploymentArgs, config.Deployment.Overrides)
	if err != nil {
		return nil, err
	}

	_, err = a.pythonBridge.Run(ctx, ai.MLClient, deploymentArgs...)
	if err != nil {
		return nil, err
	}

	// Wait for the deployment to be available. This is because it can take some time for the AI service to replicate
	// 404 response is retried up to 5 times with a 10 second delay
	err = a.waitForDeployment(ctx, scope, endpointName, deploymentName, 5, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("trying to get deployment: %w", err)
	}

	// Poll for deployment and wait till it reaches a terminal state
	onlineDeployment, err := a.pollForDeployment(ctx, scope, endpointName, deploymentName)
	if err != nil {
		return nil, err
	}

	return onlineDeployment, nil
}

// DeleteDeployments deletes all deployments of an online endpoint except the ones in filter
func (a *aiHelper) DeleteDeployments(
	ctx context.Context,
	scope *ai.Scope,
	endpointName string,
	filter []string,
) error {
	// Get existing deployments
	existingDeployments := []*armmachinelearning.OnlineDeployment{}

	deploymentsPager := a.deploymentsClient.NewListPager(scope.ResourceGroup(), scope.Workspace(), endpointName, nil)
	for deploymentsPager.More() {
		page, err := deploymentsPager.NextPage(ctx)
		if err != nil {
			return err
		}

		existingDeployments = append(existingDeployments, page.Value...)
	}

	// Delete previous deployments
	for _, existingDeployment := range existingDeployments {
		// Ignore the ones from the filter list
		if slices.Contains(filter, *existingDeployment.Name) {
			continue
		}

		// We will start the delete operation but not wait for completion
		_, err := a.deploymentsClient.BeginDelete(
			ctx,
			scope.ResourceGroup(),
			scope.Workspace(),
			endpointName,
			*existingDeployment.Name,
			nil,
		)
		if err != nil {
			return err
		}
	}

	return nil
}

// UpdateTraffic updates the traffic distribution of an online endpoint for the specified deployment
func (a *aiHelper) UpdateTraffic(
	ctx context.Context,
	scope *ai.Scope,
	endpointName string,
	deploymentName string,
) (*armmachinelearning.OnlineEndpoint, error) {
	// Get the endpoint
	getEndpointResponse, err := a.endpointsClient.Get(ctx, scope.ResourceGroup(), scope.Workspace(), endpointName, nil)
	if err != nil {
		return nil, err
	}

	onlineEndpoint := getEndpointResponse.OnlineEndpoint

	// Send all traffic to new deployment
	onlineEndpoint.Properties.Traffic = map[string]*int32{
		deploymentName: convert.RefOf(int32(100)),
	}

	poller, err := a.endpointsClient.BeginCreateOrUpdate(
		ctx,
		scope.ResourceGroup(),
		scope.Workspace(),
		endpointName,
		onlineEndpoint,
		nil,
	)
	if err != nil {
		return nil, err
	}

	updateResponse, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, err
	}

	// before moving on, we need to validate the state of the online endpoint to be updated with the
	// expected traffic (100%)
	err = retry.Do(ctx, retry.WithMaxRetries(3, retry.NewConstant(10*time.Second)),
		func(ctx context.Context) error {
			getEndpointResponse, err = a.endpointsClient.Get(
				ctx, scope.ResourceGroup(), scope.Workspace(), endpointName, nil)

			if err != nil {
				return retry.RetryableError(err)
			}
			if getEndpointResponse.OnlineEndpoint.Properties == nil {
				return retry.RetryableError(errors.New("online endpoint properties are nil"))
			}
			// check 100% traffic
			for key, trafficWeight := range getEndpointResponse.OnlineEndpoint.Properties.Traffic {
				if key == deploymentName && *trafficWeight == 100 {
					return nil
				}
			}
			return retry.RetryableError(errors.New("online endpoint traffic is not 100% yet"))
		})
	if err != nil {
		return nil, err
	}

	return &updateResponse.OnlineEndpoint, nil
}

// CreateFlow creates a new prompt flow from the specified configuration
func (a *aiHelper) CreateFlow(
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

	flowName = fmt.Sprintf("%s-%d", flowName, a.clock.Now().Unix())

	flowPath := filepath.Join(serviceConfig.Path(), config.Path)
	_, err = os.Stat(flowPath)
	if err != nil {
		return nil, err
	}

	createArgs := []string{
		"create",
		"-n", flowName,
		"-f", flowPath,
		"-s", scope.SubscriptionId(),
		"-g", scope.ResourceGroup(),
		"-w", scope.Workspace(),
	}

	createArgs, err = a.applyOverrides(createArgs, config.Overrides)
	if err != nil {
		return nil, err
	}

	result, err := a.pythonBridge.Run(ctx, ai.PromptFlowClient, createArgs...)
	if err != nil {
		return nil, fmt.Errorf("flow operation failed: %w", err)
	}

	var existingFlow *ai.Flow
	err = json.Unmarshal([]byte(result.Stdout), &existingFlow)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal flow: %w", err)
	}

	a.env.DotenvSet(AiFlowEnvVarName, flowName)

	return existingFlow, nil
}

// pollForDeployment polls for the deployment to reach a terminal state
func (a *aiHelper) pollForDeployment(
	ctx context.Context,
	scope *ai.Scope,
	endpointName string,
	deploymentName string,
) (*armmachinelearning.OnlineDeployment, error) {
	initialDelay := 3 * time.Second
	regularDelay := 10 * time.Second
	timer := time.NewTimer(initialDelay)

	terminalStates := []armmachinelearning.DeploymentProvisioningState{
		armmachinelearning.DeploymentProvisioningStateCanceled,
		armmachinelearning.DeploymentProvisioningStateFailed,
		armmachinelearning.DeploymentProvisioningStateSucceeded,
	}

	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
			// Get the deployment
			deploymentResponse, err := a.deploymentsClient.Get(
				ctx,
				scope.ResourceGroup(),
				scope.Workspace(),
				endpointName,
				deploymentName,
				nil,
			)
			if err != nil {
				timer.Stop()
				return nil, err
			}

			deploymentProps := deploymentResponse.Properties.GetOnlineDeploymentProperties()
			if slices.Contains(terminalStates, *deploymentProps.ProvisioningState) {
				timer.Stop()

				if *deploymentProps.ProvisioningState == armmachinelearning.DeploymentProvisioningStateSucceeded {
					return &deploymentResponse.OnlineDeployment, nil
				}

				return nil, fmt.Errorf("deployment completed in unsuccessful state: %s", *deploymentProps.ProvisioningState)
			}

			timer.Reset(regularDelay)
		}
	}
}

// waitForDeployment makes up to maxRetry attempts to wait for the deployment to be available. This is because it can take
// some time for the AI service to replicate a new created deployment to all regions.
func (a *aiHelper) waitForDeployment(
	ctx context.Context,
	scope *ai.Scope,
	endpointName string,
	deploymentName string,
	maxRetry uint64,
	retryDelay time.Duration,
) error {
	return retry.Do(ctx, retry.WithMaxRetries(maxRetry, retry.NewConstant(retryDelay)), func(ctx context.Context) error {
		_, err := a.deploymentsClient.Get(
			ctx,
			scope.ResourceGroup(),
			scope.Workspace(),
			endpointName,
			deploymentName,
			nil,
		)
		if err != nil {
			var sdkErr *azcore.ResponseError
			parseOk := errors.As(err, &sdkErr)
			if parseOk && sdkErr.StatusCode == http.StatusNotFound {
				// retryable error
				return retry.RetryableError(err)
			}
			// non retryable error
			return err
		}
		return nil
	})
}

// getEnvironmentVersionName returns the latest environment version name
func (a *aiHelper) getEnvironmentVersionName(
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

	envGetResponse, err := a.envContainersClient.Get(
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

// getModelVersionName returns the latest model version name
func (a *aiHelper) getModelVersionName(
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

	modelGetResponse, err := a.modelContainersClient.Get(
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

// applyOverrides applies the specified overrides to the arguments
func (a *aiHelper) applyOverrides(args []string, overrides map[string]osutil.ExpandableString) ([]string, error) {
	for key, value := range overrides {
		expandedValue, err := value.Envsubst(a.env.Getenv)
		if err != nil {
			return nil, fmt.Errorf("failed parsing override %s: %w", key, err)
		}

		args = append(args, "--set", fmt.Sprintf("%s=%s", key, expandedValue))
	}

	return args, nil
}

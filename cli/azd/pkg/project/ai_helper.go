package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/ai/promptflow"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/wbreza/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v3"
)

type AiHelper struct {
	env                *environment.Environment
	credentialProvider account.SubscriptionCredentialProvider
	armClientOptions   *arm.ClientOptions
	commandRunner      exec.CommandRunner
	flowCli            *promptflow.Cli
	credentials        azcore.TokenCredential
	initialized        bool
}

func NewAiHelper(
	env *environment.Environment,
	armClientOptions *arm.ClientOptions,
	credentialProvider account.SubscriptionCredentialProvider,
	commandRunner exec.CommandRunner,
	flowCli *promptflow.Cli,
) *AiHelper {
	return &AiHelper{
		env:                env,
		armClientOptions:   armClientOptions,
		credentialProvider: credentialProvider,
		commandRunner:      commandRunner,
		flowCli:            flowCli,
	}
}

func (a *AiHelper) init(ctx context.Context) error {
	if a.initialized {
		return nil
	}

	credentials, err := a.credentialProvider.CredentialForSubscription(ctx, a.env.GetSubscriptionId())
	if err != nil {
		return err
	}

	a.credentials = credentials
	a.initialized = true
	return nil
}

func (a *AiHelper) EnsureWorkspace(
	ctx context.Context,
	targetResource *environment.TargetResource,
	workspace osutil.ExpandableString,
) error {
	workspaceClient, err := armmachinelearning.NewWorkspacesClient(
		a.env.GetSubscriptionId(),
		a.credentials,
		a.armClientOptions,
	)
	if err != nil {
		return err
	}

	workspaceValue, err := workspace.Envsubst(a.env.Getenv)
	if err != nil {
		return fmt.Errorf("failed parsing workspace value: %w", err)
	}

	workspaceResponse, err := workspaceClient.Get(
		ctx,
		targetResource.ResourceGroupName(),
		workspaceValue,
		nil,
	)
	if err != nil {
		return err
	}

	if *workspaceResponse.Workspace.Name != workspaceValue {
		return err
	}

	return nil
}

func (a *AiHelper) CreateEnvironmentVersion(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
	config *ai.ComponentConfig,
) (*armmachinelearning.EnvironmentVersion, error) {
	if err := a.init(ctx); err != nil {
		return nil, err
	}

	yamlFilePath := filepath.Join(serviceConfig.Path(), config.Path)
	_, err := os.Stat(yamlFilePath)
	if err != nil {
		return nil, err
	}

	environmentsClient, err := armmachinelearning.NewEnvironmentContainersClient(
		a.env.GetSubscriptionId(),
		a.credentials,
		a.armClientOptions,
	)
	if err != nil {
		return nil, err
	}

	workspaceName, err := config.Workspace.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing workspace value: %w", err)
	}

	environmentName, err := config.Name.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing environment name value: %w", err)
	}

	nextVersion := "1"
	envContainerResponse, err := environmentsClient.Get(
		ctx,
		targetResource.ResourceGroupName(),
		workspaceName,
		environmentName,
		nil,
	)
	if err == nil {
		nextVersion = *envContainerResponse.Properties.NextVersion
	}

	envArgs := exec.NewRunArgs(
		"az", "ml", "environment", "create",
		"--name", environmentName,
		"--file", yamlFilePath,
		"-g", targetResource.ResourceGroupName(),
		"-w", workspaceName,
		"--version", nextVersion)

	envArgs, err = a.applyOverrides(envArgs, config.Overrides)
	if err != nil {
		return nil, err
	}

	_, err = a.commandRunner.Run(ctx, envArgs)
	if err != nil {
		return nil, err
	}

	envVersionsClient, err := armmachinelearning.NewEnvironmentVersionsClient(
		a.env.GetSubscriptionId(),
		a.credentials,
		a.armClientOptions,
	)
	if err != nil {
		return nil, err
	}

	envVersionResponse, err := envVersionsClient.Get(
		ctx,
		targetResource.ResourceGroupName(),
		workspaceName,
		environmentName,
		nextVersion,
		nil,
	)
	if err != nil {
		return nil, err
	}

	return &envVersionResponse.EnvironmentVersion, nil
}

func (a *AiHelper) CreateModelVersion(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
	config *ai.ComponentConfig,
) (*armmachinelearning.ModelVersion, error) {
	if err := a.init(ctx); err != nil {
		return nil, err
	}

	yamlFilePath := filepath.Join(serviceConfig.Path(), config.Path)
	_, err := os.Stat(yamlFilePath)
	if err != nil {
		return nil, err
	}

	workspaceName, err := config.Workspace.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing workspace value: %w", err)
	}

	modelName, err := config.Name.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing model name value: %w", err)
	}

	createModelArgs := exec.NewRunArgs(
		"az", "ml", "model", "create",
		"--name", modelName,
		"--file", yamlFilePath,
		"-g", targetResource.ResourceGroupName(),
		"-w", workspaceName,
	)

	createModelArgs, err = a.applyOverrides(createModelArgs, config.Overrides)
	if err != nil {
		return nil, err
	}

	_, err = a.commandRunner.Run(ctx, createModelArgs)
	if err != nil {
		return nil, err
	}

	modelContainerClient, err := armmachinelearning.NewModelContainersClient(
		a.env.GetSubscriptionId(),
		a.credentials,
		a.armClientOptions,
	)
	if err != nil {
		return nil, err
	}

	modelContainerResponse, err := modelContainerClient.Get(
		ctx,
		targetResource.ResourceGroupName(),
		workspaceName,
		modelName,
		nil,
	)
	if err != nil {
		return nil, err
	}

	modelContainer := &modelContainerResponse.ModelContainer

	modelVersionClient, err := armmachinelearning.NewModelVersionsClient(
		a.env.GetSubscriptionId(),
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
		targetResource.ResourceGroupName(),
		workspaceName,
		modelName,
		latestVersion,
		nil,
	)
	if err != nil {
		return nil, err
	}

	return &modelVersionResponse.ModelVersion, nil
}

func (a *AiHelper) CreateOrUpdateEndpoint(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
	config *ai.EndpointConfig,
) (*armmachinelearning.OnlineEndpoint, error) {
	if err := a.init(ctx); err != nil {
		return nil, err
	}

	workspaceName, err := config.Workspace.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing workspace value: %w", err)
	}

	endpointName, err := config.Name.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing endpoint name value: %w", err)
	}

	yamlFilePath := filepath.Join(serviceConfig.Path(), config.Path)
	_, err = os.Stat(yamlFilePath)
	if err != nil {
		return nil, err
	}

	endpointClient, err := armmachinelearning.NewOnlineEndpointsClient(
		a.env.GetSubscriptionId(),
		a.credentials,
		a.armClientOptions,
	)
	if err != nil {
		return nil, err
	}

	var endpointCreateOrUpdateArgs exec.RunArgs

	_, err = endpointClient.Get(
		ctx,
		targetResource.ResourceGroupName(),
		workspaceName,
		endpointName,
		nil,
	)
	if err == nil {
		endpointCreateOrUpdateArgs = exec.NewRunArgs(
			"az", "ml", "online-endpoint", "update",
			"--file", yamlFilePath,
			"-n", endpointName,
			"-g", targetResource.ResourceGroupName(),
			"-w", workspaceName,
		)
	} else {
		endpointCreateOrUpdateArgs = exec.NewRunArgs("az",
			"ml", "online-endpoint", "create",
			"--file", yamlFilePath,
			"-n", endpointName,
			"-g", targetResource.ResourceGroupName(),
			"-w", workspaceName,
		)
	}

	endpointCreateOrUpdateArgs, err = a.applyOverrides(endpointCreateOrUpdateArgs, config.Overrides)
	if err != nil {
		return nil, err
	}

	_, err = a.commandRunner.Run(ctx, endpointCreateOrUpdateArgs)
	if err != nil {
		return nil, err
	}

	endpointResponse, err := endpointClient.Get(
		ctx,
		targetResource.ResourceGroupName(),
		workspaceName,
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
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
	config *ai.EndpointConfig,
) (*armmachinelearning.OnlineDeployment, error) {
	if err := a.init(ctx); err != nil {
		return nil, err
	}

	workspaceName, err := config.Workspace.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing workspace value: %w", err)
	}

	environmentName, err := config.Environment.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing environment name value: %w", err)
	}

	modelName, err := config.Model.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing model name value: %w", err)
	}

	endpointName, err := config.Name.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing endpoint name value: %w", err)
	}

	yamlFilePath := filepath.Join(serviceConfig.Path(), config.Deployment.Path)
	_, err = os.Stat(yamlFilePath)
	if err != nil {
		return nil, err
	}

	envClient, err := armmachinelearning.NewEnvironmentContainersClient(
		a.env.GetSubscriptionId(),
		a.credentials,
		a.armClientOptions,
	)
	if err != nil {
		return nil, err
	}

	envGetResponse, err := envClient.Get(
		ctx,
		targetResource.ResourceGroupName(),
		workspaceName,
		environmentName,
		nil,
	)
	if err != nil {
		return nil, err
	}

	environmentContainer := envGetResponse.EnvironmentContainer

	modelClient, err := armmachinelearning.NewModelContainersClient(
		a.env.GetSubscriptionId(),
		a.credentials,
		a.armClientOptions,
	)
	if err != nil {
		return nil, err
	}

	modelGetResponse, err := modelClient.Get(
		ctx,
		targetResource.ResourceGroupName(),
		workspaceName,
		modelName,
		nil,
	)
	if err != nil {
		return nil, err
	}

	modelContainer := modelGetResponse.ModelContainer

	deploymentName := fmt.Sprintf("azd-%d", time.Now().Unix())
	modelVersionName := fmt.Sprintf(
		"azureml:%s:%s",
		*modelContainer.Name,
		*modelContainer.Properties.LatestVersion,
	)
	environmentVersionName := fmt.Sprintf(
		"azureml:%s:%s",
		*environmentContainer.Name,
		*environmentContainer.Properties.LatestVersion,
	)

	a.env.DotenvSet("FLOW_WORKSPACE_NAME", workspaceName)
	a.env.DotenvSet("FLOW_ENVIRONMENT_NAME", environmentName)
	a.env.DotenvSet("FLOW_MODEL_NAME", modelName)
	a.env.DotenvSet("FLOW_ENDPOINT_NAME", endpointName)
	a.env.DotenvSet("FLOW_DEPLOYMENT_NAME", deploymentName)

	deploymentArgs := exec.NewRunArgs("az", "ml",
		"online-deployment", "create",
		"--file", yamlFilePath,
		"--name", deploymentName,
		"--endpoint-name", endpointName,
		"--all-traffic",
		"-g", targetResource.ResourceGroupName(),
		"-w", workspaceName,
		"--set", fmt.Sprintf("environment=%s", environmentVersionName),
		"--set", fmt.Sprintf("model=%s", modelVersionName),
		"--debug",
	)

	deploymentArgs, err = a.applyOverrides(deploymentArgs, config.Deployment.Overrides)
	if err != nil {
		return nil, err
	}

	_, err = a.commandRunner.Run(ctx, deploymentArgs)
	if err != nil {
		return nil, err
	}

	deploymentsClient, err := armmachinelearning.NewOnlineDeploymentsClient(
		a.env.GetSubscriptionId(),
		a.credentials,
		a.armClientOptions,
	)
	if err != nil {
		return nil, err
	}

	deploymentResponse, err := deploymentsClient.Get(
		ctx,
		targetResource.ResourceGroupName(),
		workspaceName,
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
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
	config *promptflow.Config,
) (*promptflow.Flow, error) {
	if err := a.init(ctx); err != nil {
		return nil, err
	}

	workspaceName, err := config.Workspace.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing workspace value: %w", err)
	}

	flowName, err := config.Name.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing flow name value: %w", err)
	}

	yamlFilePath := serviceConfig.Path()
	_, err = os.Stat(yamlFilePath)
	if err != nil {
		return nil, err
	}

	flow := &promptflow.Flow{
		DisplayName: fmt.Sprintf("%s-%d", flowName, time.Now().Unix()),
		Type:        promptflow.FlowTypeChat,
		Path:        yamlFilePath,
	}

	updatedFlow, err := a.flowCli.CreateOrUpdate(
		ctx,
		workspaceName,
		targetResource.ResourceGroupName(),
		flow,
		config.Overrides,
	)
	if err != nil {
		return nil, err
	}

	return updatedFlow, nil
}

func (a *AiHelper) CreateOrUpdateConnection(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
	config *ai.ConnectionConfig,
) (*armmachinelearning.WorkspaceConnectionPropertiesV2BasicResource, error) {
	if err := a.init(ctx); err != nil {
		return nil, err
	}

	connectionName, err := config.Name.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing connection name value: %w", err)
	}

	workspaceName, err := config.Workspace.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing workspace value: %w", err)
	}

	client, err := armmachinelearning.NewWorkspaceConnectionsClient(
		a.env.GetSubscriptionId(),
		a.credentials,
		a.armClientOptions,
	)
	if err != nil {
		return nil, err
	}

	workspaceConnection, err := a.createWorkspaceConnection(config)
	if err != nil {
		return nil, fmt.Errorf("failed parsing workspace connection, %w", err)
	}

	_, err = client.Get(ctx, targetResource.ResourceGroupName(), workspaceName, connectionName, nil)
	if err == nil {
		updateBody := armmachinelearning.WorkspaceConnectionUpdateParameter{
			Properties: workspaceConnection.Properties,
		}
		updateResponse, err := client.Update(
			ctx,
			targetResource.ResourceGroupName(),
			workspaceName,
			connectionName,
			updateBody,
			nil,
		)
		if err == nil {
			workspaceConnection = &updateResponse.WorkspaceConnectionPropertiesV2BasicResource
		}
	} else {
		createResponse, err := client.Create(
			ctx,
			targetResource.ResourceGroupName(),
			workspaceName,
			connectionName,
			*workspaceConnection,
			nil,
		)
		if err != nil {
			return nil, err
		}

		workspaceConnection = &createResponse.WorkspaceConnectionPropertiesV2BasicResource
	}

	return workspaceConnection, nil
}

func (a *AiHelper) applyOverrides(args exec.RunArgs, overrides map[string]osutil.ExpandableString) (exec.RunArgs, error) {
	for key, value := range overrides {
		expandedValue, err := value.Envsubst(a.env.Getenv)
		if err != nil {
			return exec.RunArgs{}, fmt.Errorf("failed parsing environment override %s: %w", key, err)
		}

		args = args.AppendParams("--set", fmt.Sprintf("%s=%s", key, expandedValue))
	}

	return args, nil
}

func (a *AiHelper) createWorkspaceConnection(
	config *ai.ConnectionConfig,
) (*armmachinelearning.WorkspaceConnectionPropertiesV2BasicResource, error) {
	connectionName, err := config.Name.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing connection name value: %w", err)
	}

	workspaceConnection := armmachinelearning.WorkspaceConnectionPropertiesV2BasicResource{
		Name: &connectionName,
	}

	targetValue, err := config.Target.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing connection target value: %w", err)
	}

	apiKeyValue, err := config.ApiKey.Envsubst(a.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing connection api key value: %w", err)
	}

	var properties armmachinelearning.WorkspaceConnectionPropertiesV2Classification

	authType := armmachinelearning.ConnectionAuthType(config.AuthType)
	categoryType := armmachinelearning.ConnectionCategory(config.Category)

	switch config.AuthType {
	case armmachinelearning.ConnectionAuthTypeAPIKey:
		properties = &armmachinelearning.APIKeyAuthWorkspaceConnectionProperties{
			AuthType: &authType,
			Category: &categoryType,
			Target:   &targetValue,
			Credentials: &armmachinelearning.WorkspaceConnectionAPIKey{
				Key: &apiKeyValue,
			},
			Metadata: config.Metadata,
		}
	case armmachinelearning.ConnectionAuthTypeCustomKeys:
		properties = &armmachinelearning.CustomKeysWorkspaceConnectionProperties{
			AuthType: &authType,
			Category: &categoryType,
			Target:   &targetValue,
			Credentials: &armmachinelearning.CustomKeys{
				Keys: map[string]*string{
					"key": &apiKeyValue,
				},
			},
			Metadata: config.Metadata,
		}
	}

	workspaceConnection.Properties = properties

	return &workspaceConnection, nil
}

package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type AiEndpoint struct {
	env                *environment.Environment
	credentialProvider account.SubscriptionCredentialProvider
	armClientOptions   *arm.ClientOptions
	commandRunner      exec.CommandRunner
}

func NewAiEndpoint(
	env *environment.Environment,
	armClientOptions *arm.ClientOptions,
	credentialProvider account.SubscriptionCredentialProvider,
	commandRunner exec.CommandRunner,
) ServiceTarget {
	return &AiEndpoint{
		env:                env,
		armClientOptions:   armClientOptions,
		credentialProvider: credentialProvider,
		commandRunner:      commandRunner,
	}
}

func (m *AiEndpoint) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	// Implement the Initialize method here.
	return nil
}

func (m *AiEndpoint) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	// Implement the RequiredExternalTools method here.
	return nil
}

func (m *AiEndpoint) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	frameworkPackageOutput *ServicePackageResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	// Implement the Package method here.
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
		task.SetResult(&ServicePackageResult{})
	})
}

func (m *AiEndpoint) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	servicePackage *ServicePackageResult,
	targetResource *environment.TargetResource,
) *async.TaskWithProgress[*ServiceDeployResult, ServiceProgress] {
	// Implement the Deploy method here.
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServiceDeployResult, ServiceProgress]) {
		credentials, err := m.credentialProvider.CredentialForSubscription(ctx, m.env.GetSubscriptionId())
		if err != nil {
			task.SetError(err)
			return
		}

		workspaceClient, err := armmachinelearning.NewWorkspacesClient(m.env.GetSubscriptionId(), credentials, m.armClientOptions)
		if err != nil {
			task.SetError(err)
			return
		}

		workspaceResponse, err := workspaceClient.Get(ctx, targetResource.ResourceGroupName(), serviceConfig.Ai.Workspace, nil)
		if err != nil {
			task.SetError(err)
			return
		}

		if *workspaceResponse.Workspace.Name != serviceConfig.Ai.Workspace {
			task.SetError(errors.New("Workspace not found"))
			return
		}

		yamlFilePath := filepath.Join(serviceConfig.Path(), serviceConfig.Ai.Path)
		_, err = os.Stat(yamlFilePath)
		if err != nil {
			task.SetError(err)
			return
		}

		endpointClient, err := armmachinelearning.NewOnlineEndpointsClient(m.env.GetSubscriptionId(), credentials, m.armClientOptions)
		if err != nil {
			task.SetError(err)
			return
		}

		var endpointCreateOrUpdateArgs exec.RunArgs

		_, err = endpointClient.Get(ctx, targetResource.ResourceGroupName(), serviceConfig.Ai.Workspace, serviceConfig.Ai.Name, nil)
		if err == nil {
			endpointCreateOrUpdateArgs = exec.NewRunArgs("az",
				"ml",
				"online-endpoint",
				"update",
				"--file", yamlFilePath,
				"-n", serviceConfig.Ai.Name,
				"-g", targetResource.ResourceGroupName(),
				"-w", serviceConfig.Ai.Workspace,
			)
		} else {
			endpointCreateOrUpdateArgs = exec.NewRunArgs("az",
				"ml",
				"online-endpoint",
				"create",
				"--file", yamlFilePath,
				"-n", serviceConfig.Ai.Name,
				"-g", targetResource.ResourceGroupName(),
				"-w", serviceConfig.Ai.Workspace,
			)
		}

		// echo "Registering PromptFlow as a model in Azure ML..."
		// az ml model create --file deployment/chat-model.yaml  -g $AZURE_RESOURCE_GROUP -w $AZURE_MLPROJECT_NAME
		task.SetProgress(NewServiceProgress("Creating/updating endpoint"))
		_, err = m.commandRunner.Run(ctx, endpointCreateOrUpdateArgs)
		if err != nil {
			task.SetError(err)
			return
		}

		endpointResponse, err := endpointClient.Get(ctx, targetResource.ResourceGroupName(), serviceConfig.Ai.Workspace, serviceConfig.Ai.Name, nil)
		if err != nil {
			task.SetError(err)
			return
		}

		endpoint := &endpointResponse.OnlineEndpoint

		envClient, err := armmachinelearning.NewEnvironmentContainersClient(m.env.GetSubscriptionId(), credentials, m.armClientOptions)
		if err != nil {
			task.SetError(err)
			return
		}

		envGetResponse, err := envClient.Get(ctx, targetResource.ResourceGroupName(), serviceConfig.Ai.Workspace, serviceConfig.Ai.Environment, nil)
		if err != nil {
			task.SetError(err)
			return
		}

		envVersionsClient, err := armmachinelearning.NewEnvironmentVersionsClient(m.env.GetSubscriptionId(), credentials, m.armClientOptions)
		if err != nil {
			task.SetError(err)
			return
		}

		envVersionResponse, err := envVersionsClient.Get(ctx, targetResource.ResourceGroupName(), serviceConfig.Ai.Workspace, serviceConfig.Ai.Environment, *envGetResponse.Properties.LatestVersion, nil)
		if err != nil {
			task.SetError(err)
			return
		}

		modelClient, err := armmachinelearning.NewModelContainersClient(m.env.GetSubscriptionId(), credentials, m.armClientOptions)
		if err != nil {
			task.SetError(err)
			return
		}

		modelGetResponse, err := modelClient.Get(ctx, targetResource.ResourceGroupName(), serviceConfig.Ai.Workspace, serviceConfig.Ai.Model, nil)
		if err != nil {
			task.SetError(err)
			return
		}

		modelContainer := &modelGetResponse.ModelContainer

		modelVersionsClient, err := armmachinelearning.NewModelVersionsClient(m.env.GetSubscriptionId(), credentials, m.armClientOptions)
		if err != nil {
			task.SetError(err)
			return
		}

		_, err = modelVersionsClient.Get(ctx, targetResource.ResourceGroupName(), serviceConfig.Ai.Workspace, serviceConfig.Ai.Model, *modelGetResponse.Properties.LatestVersion, nil)
		if err != nil {
			task.SetError(err)
			return
		}

		modelName := fmt.Sprintf("azureml:%s:%s", *modelContainer.Name, *modelContainer.Properties.LatestVersion)
		deploymentName := fmt.Sprintf("%s-azd-%d", serviceConfig.Ai.Name, time.Now().Unix())

		deploymentArgs := exec.NewRunArgs("az", "ml",
			"online-deployment", "create",
			"--file", yamlFilePath,
			"--name", deploymentName,
			"--endpoint-name", *endpoint.Name,
			"--all-traffic",
			"-g", targetResource.ResourceGroupName(),
			"-w", serviceConfig.Ai.Workspace,
			"--set", fmt.Sprintf("environment.image=%s", *envVersionResponse.Properties.Image),
			"--set", fmt.Sprintf("model=%s", modelName),
		)

		task.SetProgress(NewServiceProgress("Deploying endpoint"))
		_, err = m.commandRunner.Run(ctx, deploymentArgs)

		endpoints := []string{
			fmt.Sprintf("Scoring URI: %s", *endpoint.Properties.ScoringURI),
			fmt.Sprintf("Swagger URI: %s", *endpoint.Properties.SwaggerURI),
		}

		task.SetResult(&ServiceDeployResult{
			Package:   servicePackage,
			Details:   endpoint,
			Endpoints: endpoints,
		})
	})
}

func (m *AiEndpoint) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	// Implement the Endpoints method here.
	return []string{}, nil
}

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

		workspaceClient, err := armmachinelearning.NewWorkspacesClient(
			m.env.GetSubscriptionId(),
			credentials,
			m.armClientOptions,
		)
		if err != nil {
			task.SetError(err)
			return
		}

		workspaceResponse, err := workspaceClient.Get(
			ctx,
			targetResource.ResourceGroupName(),
			serviceConfig.Ai.Workspace,
			nil,
		)
		if err != nil {
			task.SetError(err)
			return
		}

		if *workspaceResponse.Workspace.Name != serviceConfig.Ai.Workspace {
			task.SetError(errors.New("Workspace not found"))
			return
		}

		endpointYamlFilePath := filepath.Join(serviceConfig.Path(), serviceConfig.Ai.Path)
		_, err = os.Stat(endpointYamlFilePath)
		if err != nil {
			task.SetError(err)
			return
		}

		deploymentYamlFilePath := filepath.Join(serviceConfig.Path(), serviceConfig.Ai.DeploymentPath)
		_, err = os.Stat(deploymentYamlFilePath)
		if err != nil {
			task.SetError(err)
			return
		}

		endpointClient, err := armmachinelearning.NewOnlineEndpointsClient(
			m.env.GetSubscriptionId(),
			credentials,
			m.armClientOptions,
		)
		if err != nil {
			task.SetError(err)
			return
		}

		var endpointCreateOrUpdateArgs exec.RunArgs

		_, err = endpointClient.Get(
			ctx,
			targetResource.ResourceGroupName(),
			serviceConfig.Ai.Workspace,
			serviceConfig.Ai.Name,
			nil,
		)
		if err == nil {
			endpointCreateOrUpdateArgs = exec.NewRunArgs(
				"az", "ml", "online-endpoint", "update",
				"--file", endpointYamlFilePath,
				"-n", serviceConfig.Ai.Name,
				"-g", targetResource.ResourceGroupName(),
				"-w", serviceConfig.Ai.Workspace,
			)
		} else {
			endpointCreateOrUpdateArgs = exec.NewRunArgs("az",
				"ml", "online-endpoint", "create",
				"--file", endpointYamlFilePath,
				"-n", serviceConfig.Ai.Name,
				"-g", targetResource.ResourceGroupName(),
				"-w", serviceConfig.Ai.Workspace,
			)
		}

		task.SetProgress(NewServiceProgress("Creating/updating endpoint"))
		_, err = m.commandRunner.Run(ctx, endpointCreateOrUpdateArgs)
		if err != nil {
			task.SetError(err)
			return
		}

		endpointResponse, err := endpointClient.Get(
			ctx,
			targetResource.ResourceGroupName(),
			serviceConfig.Ai.Workspace,
			serviceConfig.Ai.Name,
			nil,
		)
		if err != nil {
			task.SetError(err)
			return
		}

		endpoint := &endpointResponse.OnlineEndpoint

		envClient, err := armmachinelearning.NewEnvironmentContainersClient(
			m.env.GetSubscriptionId(),
			credentials,
			m.armClientOptions,
		)
		if err != nil {
			task.SetError(err)
			return
		}

		envGetResponse, err := envClient.Get(
			ctx,
			targetResource.ResourceGroupName(),
			serviceConfig.Ai.Workspace,
			serviceConfig.Ai.Environment,
			nil,
		)
		if err != nil {
			task.SetError(err)
			return
		}

		environmentContainer := envGetResponse.EnvironmentContainer

		modelClient, err := armmachinelearning.NewModelContainersClient(
			m.env.GetSubscriptionId(),
			credentials,
			m.armClientOptions,
		)
		if err != nil {
			task.SetError(err)
			return
		}

		modelGetResponse, err := modelClient.Get(
			ctx,
			targetResource.ResourceGroupName(),
			serviceConfig.Ai.Workspace,
			serviceConfig.Ai.Model,
			nil,
		)
		if err != nil {
			task.SetError(err)
			return
		}

		modelContainer := modelGetResponse.ModelContainer

		deploymentName := fmt.Sprintf("azd-%d", time.Now().Unix())
		modelName := fmt.Sprintf(
			"azureml:%s:%s",
			*modelContainer.Name,
			*modelContainer.Properties.LatestVersion,
		)
		environmentName := fmt.Sprintf(
			"azureml:%s:%s",
			*environmentContainer.Name,
			*environmentContainer.Properties.LatestVersion,
		)

		deploymentArgs := exec.NewRunArgs("az", "ml",
			"online-deployment", "create",
			"--file", deploymentYamlFilePath,
			"--name", deploymentName,
			"--endpoint-name", *endpoint.Name,
			"--all-traffic",
			"-g", targetResource.ResourceGroupName(),
			"-w", serviceConfig.Ai.Workspace,
			"--set", fmt.Sprintf("environment=%s", environmentName),
			"--set", fmt.Sprintf("model=%s", modelName),
		)

		task.SetProgress(NewServiceProgress("Deploying to endpoint"))
		_, err = m.commandRunner.Run(ctx, deploymentArgs)
		if err != nil {
			task.SetError(err)
			return
		}

		deploymentsClient, err := armmachinelearning.NewOnlineDeploymentsClient(
			m.env.GetSubscriptionId(),
			credentials,
			m.armClientOptions,
		)
		if err != nil {
			task.SetError(err)
			return
		}

		deploymentResponse, err := deploymentsClient.Get(
			ctx,
			targetResource.ResourceGroupName(),
			serviceConfig.Ai.Workspace,
			serviceConfig.Ai.Name,
			deploymentName,
			nil,
		)
		if err != nil {
			task.SetError(err)
			return
		}

		endpoints := []string{
			fmt.Sprintf("Scoring URI: %s", *endpoint.Properties.ScoringURI),
			fmt.Sprintf("Swagger URI: %s", *endpoint.Properties.SwaggerURI),
		}

		task.SetResult(&ServiceDeployResult{
			Package:   servicePackage,
			Details:   &deploymentResponse.OnlineDeployment,
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

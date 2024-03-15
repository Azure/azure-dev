package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type AiEnvironment struct {
	env                *environment.Environment
	credentialProvider account.SubscriptionCredentialProvider
	armClientOptions   *arm.ClientOptions
	commandRunner      exec.CommandRunner
}

func NewAiEnvironment(
	env *environment.Environment,
	armClientOptions *arm.ClientOptions,
	credentialProvider account.SubscriptionCredentialProvider,
	commandRunner exec.CommandRunner,
) ServiceTarget {
	return &AiEnvironment{
		env:                env,
		armClientOptions:   armClientOptions,
		credentialProvider: credentialProvider,
		commandRunner:      commandRunner,
	}
}

func (m *AiEnvironment) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	// Implement the Initialize method here.
	return nil
}

func (m *AiEnvironment) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	// Implement the RequiredExternalTools method here.
	return nil
}

func (m *AiEnvironment) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	frameworkPackageOutput *ServicePackageResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	// Implement the Package method here.
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
		task.SetResult(&ServicePackageResult{})
	})
}

func (m *AiEnvironment) Deploy(
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

		environmentsClient, err := armmachinelearning.NewEnvironmentContainersClient(m.env.GetSubscriptionId(), credentials, m.armClientOptions)
		if err != nil {
			task.SetError(err)
			return
		}

		nextVersion := "1"
		envContainerResponse, err := environmentsClient.Get(ctx, targetResource.ResourceGroupName(), serviceConfig.Ai.Workspace, serviceConfig.Ai.Name, nil)
		if err == nil {
			nextVersion = *envContainerResponse.Properties.NextVersion
		}

		// az ml environment create --file deployment/docker/environment.yml --resource-group $AZURE_RESOURCE_GROUP --workspace-name $AZURE_MLPROJECT_NAME --version $new_version
		envArgs := exec.NewRunArgs("az", "ml", "environment", "create",
			"--file", yamlFilePath,
			"-g", targetResource.ResourceGroupName(),
			"-w", serviceConfig.Ai.Workspace,
			"--version", nextVersion)

		_, err = m.commandRunner.Run(ctx, envArgs)
		if err != nil {
			task.SetError(err)
			return
		}

		envVersionsClient, err := armmachinelearning.NewEnvironmentVersionsClient(m.env.GetSubscriptionId(), credentials, m.armClientOptions)
		if err != nil {
			task.SetError(err)
			return
		}

		envVersionResponse, err := envVersionsClient.Get(ctx, targetResource.ResourceGroupName(), serviceConfig.Ai.Workspace, serviceConfig.Ai.Name, nextVersion, nil)
		if err != nil {
			task.SetError(err)
			return
		}

		envVersion := &envVersionResponse.EnvironmentVersion

		task.SetResult(&ServiceDeployResult{
			Package: servicePackage,
			Details: envVersion,
		})
	})
}

func (m *AiEnvironment) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	// Implement the Endpoints method here.
	return []string{}, nil
}

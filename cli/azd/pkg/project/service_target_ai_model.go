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

type AiModel struct {
	env                *environment.Environment
	credentialProvider account.SubscriptionCredentialProvider
	armClientOptions   *arm.ClientOptions
	commandRunner      exec.CommandRunner
}

func NewAiModel(
	env *environment.Environment,
	armClientOptions *arm.ClientOptions,
	credentialProvider account.SubscriptionCredentialProvider,
	commandRunner exec.CommandRunner,
) ServiceTarget {
	return &AiModel{
		env:                env,
		armClientOptions:   armClientOptions,
		credentialProvider: credentialProvider,
		commandRunner:      commandRunner,
	}
}

func (m *AiModel) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	// Implement the Initialize method here.
	return nil
}

func (m *AiModel) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	// Implement the RequiredExternalTools method here.
	return nil
}

func (m *AiModel) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	frameworkPackageOutput *ServicePackageResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	// Implement the Package method here.
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
		task.SetResult(&ServicePackageResult{})
	})
}

func (m *AiModel) Deploy(
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

		// echo "Registering PromptFlow as a model in Azure ML..."
		// az ml model create --file deployment/chat-model.yaml  -g $AZURE_RESOURCE_GROUP -w $AZURE_MLPROJECT_NAME
		createModelArgs := exec.NewRunArgs("az",
			"ml",
			"model",
			"create",
			"--file", yamlFilePath,
			"-g", targetResource.ResourceGroupName(),
			"-w", serviceConfig.Ai.Workspace,
		)

		_, err = m.commandRunner.Run(ctx, createModelArgs)
		if err != nil {
			task.SetError(err)
			return
		}

		modelContainerClient, err := armmachinelearning.NewModelContainersClient(m.env.GetSubscriptionId(), credentials, m.armClientOptions)
		if err != nil {
			task.SetError(err)
			return
		}

		modelContainerResponse, err := modelContainerClient.Get(ctx, targetResource.ResourceGroupName(), serviceConfig.Ai.Workspace, serviceConfig.Ai.Name, nil)
		if err != nil {
			task.SetError(err)
			return
		}

		modelContainer := &modelContainerResponse.ModelContainer

		modelVersionClient, err := armmachinelearning.NewModelVersionsClient(m.env.GetSubscriptionId(), credentials, m.armClientOptions)
		if err != nil {
			task.SetError(err)
			return
		}

		latestVersion := "1"
		if modelContainer.Properties.LatestVersion != nil {
			latestVersion = *modelContainer.Properties.LatestVersion
		}

		modelVersionResponse, err := modelVersionClient.Get(ctx, targetResource.ResourceGroupName(), serviceConfig.Ai.Workspace, serviceConfig.Ai.Name, latestVersion, nil)
		if err != nil {
			task.SetError(err)
			return
		}

		modelVersion := &modelVersionResponse.ModelVersion

		endpoints := []string{
			*modelVersion.Properties.ModelURI,
		}

		task.SetResult(&ServiceDeployResult{
			Package:   servicePackage,
			Details:   modelVersion,
			Endpoints: endpoints,
		})
	})
}

func (m *AiModel) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	// Implement the Endpoints method here.
	return []string{}, nil
}

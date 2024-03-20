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
	"github.com/azure/azure-dev/cli/azd/pkg/ai/promptflow"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type AiFlow struct {
	env                *environment.Environment
	credentialProvider account.SubscriptionCredentialProvider
	armClientOptions   *arm.ClientOptions
	commandRunner      exec.CommandRunner
	flowCli            *promptflow.Cli
}

func NewAiFlow(
	env *environment.Environment,
	armClientOptions *arm.ClientOptions,
	credentialProvider account.SubscriptionCredentialProvider,
	commandRunner exec.CommandRunner,
	flowCli *promptflow.Cli,
) ServiceTarget {
	return &AiFlow{
		env:                env,
		armClientOptions:   armClientOptions,
		credentialProvider: credentialProvider,
		commandRunner:      commandRunner,
		flowCli:            flowCli,
	}
}

func (m *AiFlow) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	// Implement the Initialize method here.
	return nil
}

func (m *AiFlow) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	// Implement the RequiredExternalTools method here.
	return nil
}

func (m *AiFlow) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	frameworkPackageOutput *ServicePackageResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	// Implement the Package method here.
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
		task.SetResult(&ServicePackageResult{})
	})
}

func (m *AiFlow) Deploy(
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

		yamlFilePath := filepath.Join(serviceConfig.Path(), serviceConfig.Ai.Path)
		_, err = os.Stat(yamlFilePath)
		if err != nil {
			task.SetError(err)
			return
		}

		flow := &promptflow.Flow{
			DisplayName: fmt.Sprintf("%s-%d", serviceConfig.Ai.Name, time.Now().Unix()),
			Type:        promptflow.FlowTypeChat,
			Path:        yamlFilePath,
		}

		updatedFlow, err := m.flowCli.CreateOrUpdate(
			ctx,
			serviceConfig.Ai.Workspace,
			targetResource.ResourceGroupName(),
			flow,
			nil,
		)
		if err != nil {
			task.SetError(err)
			return
		}

		endpoints := []string{
			updatedFlow.Code,
		}

		task.SetResult(&ServiceDeployResult{
			Package:   servicePackage,
			Details:   updatedFlow,
			Endpoints: endpoints,
		})
	})
}

func (m *AiFlow) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	// Implement the Endpoints method here.
	return []string{}, nil
}

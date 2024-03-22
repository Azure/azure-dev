package project

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type AiEndpoint struct {
	aiHelper *AiHelper
}

func NewAiEndpoint(aiHelper *AiHelper) ServiceTarget {
	return &AiEndpoint{
		aiHelper: aiHelper,
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
		endpointConfig, err := ai.ParseEndpointConfig(serviceConfig.Config)
		if err != nil {
			task.SetError(err)
			return
		}

		if err := m.aiHelper.EnsureWorkspace(ctx, targetResource, endpointConfig.Workspace); err != nil {
			task.SetError(err)
			return
		}

		_, err = m.aiHelper.CreateOrUpdateEndpoint(ctx, serviceConfig, targetResource, endpointConfig)
		if err != nil {
			task.SetError(err)
			return
		}

		deployment, err := m.aiHelper.DeployToEndpoint(ctx, serviceConfig, targetResource, endpointConfig)
		if err != nil {
			task.SetError(err)
			return
		}

		task.SetResult(&ServiceDeployResult{
			Package: servicePackage,
			Details: deployment,
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

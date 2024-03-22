package project

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type AiModel struct {
	aiHelper *AiHelper
}

func NewAiModel(
	aiHelper *AiHelper,
) ServiceTarget {
	return &AiModel{
		aiHelper: aiHelper,
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
		modelConfig, err := ai.ParseComponentConfig(serviceConfig.Config)
		if err != nil {
			task.SetError(err)
			return
		}

		if err := m.aiHelper.EnsureWorkspace(ctx, targetResource, modelConfig.Workspace); err != nil {
			task.SetError(err)
			return
		}

		environmentVersion, err := m.aiHelper.CreateModelVersion(ctx, serviceConfig, targetResource, modelConfig)
		if err != nil {
			task.SetError(err)
			return
		}

		task.SetResult(&ServiceDeployResult{
			Package: servicePackage,
			Details: environmentVersion,
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

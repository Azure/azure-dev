package project

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type AiEnvironment struct {
	aiHelper *AiHelper
}

func NewAiEnvironment(
	aiHelper *AiHelper,
) ServiceTarget {
	return &AiEnvironment{
		aiHelper: aiHelper,
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
		envConfig, err := ai.ParseComponentConfig(serviceConfig.Config)
		if err != nil {
			task.SetError(err)
			return
		}

		if err := m.aiHelper.EnsureWorkspace(ctx, targetResource, envConfig.Workspace); err != nil {
			task.SetError(err)
			return
		}

		environmentVersion, err := m.aiHelper.CreateEnvironmentVersion(ctx, serviceConfig, targetResource, envConfig)
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

func (m *AiEnvironment) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	// Implement the Endpoints method here.
	return []string{}, nil
}

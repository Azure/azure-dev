package project

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type NoneProject struct {
}

func NewNoneProject() FrameworkService {
	return &NoneProject{}
}

func (n *NoneProject) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{}
}

func (n *NoneProject) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

func (n *NoneProject) Requirements() FrameworkRequirements {
	return FrameworkRequirements{
		Package: FrameworkPackageRequirements{
			RequireRestore: false,
			RequireBuild:   false,
			SkipPackage:    true,
		},
	}
}

func (n *NoneProject) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceRestoreResult, ServiceProgress]) {
			task.SetResult(&ServiceRestoreResult{})
		},
	)

}

func (m *NoneProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	restoreOutput *ServiceRestoreResult,
) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceBuildResult, ServiceProgress]) {
			task.SetResult(&ServiceBuildResult{
				Restore:         restoreOutput,
				BuildOutputPath: serviceConfig.Path(),
			})
		},
	)
}

func (m *NoneProject) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
			task.SetResult(&ServicePackageResult{
				Build:       buildOutput,
				PackagePath: serviceConfig.Path(),
			})
		},
	)
}

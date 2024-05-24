package project

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type dotNetContainerPublishProject struct {
	inner FrameworkService
}

func NewDotNetContianerPublishProject() CompositeFrameworkService {
	return &dotNetContainerPublishProject{}
}

// Gets a list of the required external tools for the framework service
func (dp *dotNetContainerPublishProject) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	return dp.inner.RequiredExternalTools(ctx)
}

// Initializes the framework service for the specified service configuration
// This is useful if the framework needs to subscribe to any service events
func (dp *dotNetContainerPublishProject) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return dp.inner.Initialize(ctx, serviceConfig)
}

// Gets the requirements for the language or framework service.
// This enables more fine grain control on whether the language / framework
// supports or requires lifecycle commands such as restore, build, and package
func (dp *dotNetContainerPublishProject) Requirements() FrameworkRequirements {
	return FrameworkRequirements{
		Package: FrameworkPackageRequirements{
			RequireRestore: false,
			RequireBuild:   false,
		},
	}
}

// Restores dependencies for the framework service
func (dp *dotNetContainerPublishProject) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress] {
	return dp.inner.Restore(ctx, serviceConfig)
}

// Builds the source for the framework service
func (dp *dotNetContainerPublishProject) Build(
	ctx context.Context, serviceConfig *ServiceConfig,
	restoreOutput *ServiceRestoreResult,
) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress] {
	return dp.inner.Build(ctx, serviceConfig, restoreOutput)
}

// Packages the source suitable for deployment
// This may optionally perform a rebuild internally depending on the language/framework requirements
func (dp *dotNetContainerPublishProject) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
		task.SetResult(&ServicePackageResult{
			Build:       buildOutput,
			PackagePath: "",
			Details: &dotNetSdkPublishConfiguration{
				ProjectPath: serviceConfig.Path(),
			},
		})

		return
	})
}

func (dp *dotNetContainerPublishProject) SetSource(inner FrameworkService) {
	dp.inner = inner
}

type dotNetSdkPublishConfiguration struct {
	ProjectPath string
}

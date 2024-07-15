package project

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// NewNoOpProject creates a new instance of a no-op project, which implements the FrameworkService interface
// but does not perform any actions.
func NewNoOpProject(env *environment.Environment) FrameworkService {
	return &noOpProject{}
}

func (n *noOpProject) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{}
}

func (n *noOpProject) Requirements() FrameworkRequirements {
	return FrameworkRequirements{
		Package: FrameworkPackageRequirements{
			RequireRestore: false,
			RequireBuild:   false,
		},
	}
}

func (n *noOpProject) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

func (n *noOpProject) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	_ *async.Progress[ServiceProgress],
) (*ServiceRestoreResult, error) {
	return &ServiceRestoreResult{}, nil
}

func (n *noOpProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	restoreOutput *ServiceRestoreResult,
	progress *async.Progress[ServiceProgress],
) (*ServiceBuildResult, error) {
	return &ServiceBuildResult{}, nil
}

func (n *noOpProject) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	return &ServicePackageResult{}, nil
}

type noOpProject struct{}

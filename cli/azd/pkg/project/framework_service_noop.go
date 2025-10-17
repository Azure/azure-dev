// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

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

func (n *noOpProject) RequiredExternalTools(_ context.Context, _ *ServiceConfig) []tools.ExternalTool {
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
	serviceContext *ServiceContext,
	_ *async.Progress[ServiceProgress],
) (*ServiceRestoreResult, error) {
	return &ServiceRestoreResult{}, nil
}

func (n *noOpProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServiceBuildResult, error) {
	return &ServiceBuildResult{}, nil
}

func (n *noOpProject) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	return &ServicePackageResult{}, nil
}

type noOpProject struct{}

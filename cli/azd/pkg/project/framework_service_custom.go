// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type customProject struct {
	env *environment.Environment
}

// NewCustomProject creates a new instance of the Custom project
func NewCustomProject(env *environment.Environment) FrameworkService {
	return &customProject{
		env: env,
	}
}

func (pp *customProject) Requirements() FrameworkRequirements {
	return FrameworkRequirements{
		// Custom projects do not require compilation and will just package the raw source files
		Package: FrameworkPackageRequirements{
			RequireRestore: false,
			RequireBuild:   false,
		},
	}
}

// Gets the required external tools for the project
func (pp *customProject) RequiredExternalTools(_ context.Context, _ *ServiceConfig) []tools.ExternalTool {
	return []tools.ExternalTool{}
}

// Initializes the Python project
func (pp *customProject) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

// Restores the project dependencies using PIP requirements.txt
func (pp *customProject) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	progress *async.Progress[ServiceProgress],
) (*ServiceRestoreResult, error) {
	return &ServiceRestoreResult{
		Details: "Custom service - no restore required",
	}, nil
}

// Build for Python apps performs a no-op and returns the service path with an optional output path when specified.
func (pp *customProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	restoreOutput *ServiceRestoreResult,
	progress *async.Progress[ServiceProgress],
) (*ServiceBuildResult, error) {
	return &ServiceBuildResult{
		Details: "Custom service - no build required",
	}, nil
}

func (pp *customProject) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	return &ServicePackageResult{
		Details: "Custom service - no package required",
	}, nil
}

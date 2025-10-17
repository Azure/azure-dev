// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type customProject struct {
	env *environment.Environment
}

// NewCustomProject creates a new instance of the custom language project
func NewCustomProject(env *environment.Environment) FrameworkService {
	return &customProject{
		env: env,
	}
}

func (pp *customProject) Requirements() FrameworkRequirements {
	return FrameworkRequirements{
		// Custom language projects should run all commands when packaging
		Package: FrameworkPackageRequirements{
			RequireRestore: true,
			RequireBuild:   true,
		},
	}
}

// Gets tan empty slice of external tools for the project
func (pp *customProject) RequiredExternalTools(_ context.Context, _ *ServiceConfig) []tools.ExternalTool {
	return []tools.ExternalTool{}
}

// Initializes the custom language project
func (pp *customProject) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

// Restores the project dependencies
func (pp *customProject) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServiceRestoreResult, error) {
	return &ServiceRestoreResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     serviceConfig.Path(),
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"projectPath": serviceConfig.Path(),
					"framework":   "custom",
				},
			},
		},
	}, nil
}

// Build for custom language apps performs a no-op and returns the service path with an optional output path when specified.
func (pp *customProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServiceBuildResult, error) {
	return &ServiceBuildResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     serviceConfig.Path(),
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"buildPath": serviceConfig.Path(),
					"framework": "custom",
				},
			},
		},
	}, nil
}

func (pp *customProject) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	if serviceConfig.OutputPath == "" {
		return nil, fmt.Errorf("'dist' required for custom language")
	}

	// Create directory artifact for custom language output
	return &ServicePackageResult{
		Artifacts: ArtifactCollection{{
			Kind:         ArtifactKindDirectory,
			Location:     serviceConfig.OutputPath,
			LocationKind: LocationKindLocal,
			Metadata: map[string]string{
				"language":  "custom",
				"framework": "custom",
			},
		}},
	}, nil
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/npm"
)

type npmProject struct {
	env *environment.Environment
	cli *npm.Cli
}

// NewNpmProject creates a new instance of a NPM project
func NewNpmProject(cli *npm.Cli, env *environment.Environment) FrameworkService {
	return &npmProject{
		env: env,
		cli: cli,
	}
}

func (np *npmProject) Requirements() FrameworkRequirements {
	return FrameworkRequirements{
		Package: FrameworkPackageRequirements{
			// NPM requires a restore before running any NPM scripts
			RequireRestore: true,
			RequireBuild:   false,
		},
	}
}

// Gets the required external tools for the project
func (np *npmProject) RequiredExternalTools(_ context.Context, _ *ServiceConfig) []tools.ExternalTool {
	return []tools.ExternalTool{np.cli}
}

// Initializes the NPM project
func (np *npmProject) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

// Restores dependencies for the NPM project using npm install command
func (np *npmProject) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServiceRestoreResult, error) {
	progress.SetProgress(NewServiceProgress("Installing NPM dependencies"))
	if err := np.cli.Install(ctx, serviceConfig.Path()); err != nil {
		return nil, err
	}

	// Create restore artifact for the project directory with node_modules
	return &ServiceRestoreResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     serviceConfig.Path(),
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"projectPath":  serviceConfig.Path(),
					"framework":    "npm",
					"dependencies": "node_modules",
				},
			},
		},
	}, nil
}

// Builds the project executing the npm `build` script defined within the project package.json
func (np *npmProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServiceBuildResult, error) {
	// Exec custom `build` script if available
	// If `build`` script is not defined in the package.json the NPM script will NOT fail
	progress.SetProgress(NewServiceProgress("Running NPM build script"))
	if err := np.cli.RunScript(ctx, serviceConfig.Path(), "build"); err != nil {
		return nil, err
	}

	buildSource := serviceConfig.Path()

	if serviceConfig.OutputPath != "" {
		buildSource = filepath.Join(buildSource, serviceConfig.OutputPath)
	}

	// Create build artifact for npm build output
	return &ServiceBuildResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     buildSource,
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"buildSource": buildSource,
					"framework":   "npm",
					"outputPath":  serviceConfig.OutputPath,
				},
			},
		},
	}, nil
}

func (np *npmProject) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	progress.SetProgress(NewServiceProgress("Running NPM package script"))

	// Long term this script we call should better align with our inner-loop scenarios
	// Keeping this defaulted to `build` will create confusion for users when we start to support
	// both local dev / debug builds and production bundled builds
	if err := np.cli.RunScript(ctx, serviceConfig.Path(), "build"); err != nil {
		return nil, err
	}

	// Copy directory rooted by dist to package root.
	packagePath := serviceConfig.Path()
	// Get package path from build artifacts
	if artifact, found := serviceContext.Build.FindFirst(WithKind(ArtifactKindDirectory)); found {
		packagePath = artifact.Location
	}
	if packagePath == serviceConfig.Path() && serviceConfig.OutputPath != "" {
		packagePath = filepath.Join(serviceConfig.Path(), serviceConfig.OutputPath)
	}

	if entries, err := os.ReadDir(packagePath); err != nil || len(entries) == 0 {
		return nil, fmt.Errorf(
			//nolint:lll
			"package source '%s' is empty or does not exist. If your service has custom packaging requirements create "+
				"an NPM script named 'build' within your package.json and ensure your package artifacts are written to "+
				"the '%s' directory",
			packagePath,
			packagePath,
		)
	}

	// Create package artifact for npm package output
	return &ServicePackageResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     packagePath,
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"packagePath": packagePath,
				},
			},
		},
	}, nil
}

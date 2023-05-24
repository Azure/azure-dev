// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/messaging"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/npm"
)

type npmProject struct {
	env       *environment.Environment
	cli       npm.NpmCli
	publisher messaging.Publisher
}

// NewNpmProject creates a new instance of a NPM project
func NewNpmProject(
	cli npm.NpmCli,
	env *environment.Environment,
	publisher messaging.Publisher,
) FrameworkService {
	return &npmProject{
		env:       env,
		cli:       cli,
		publisher: publisher,
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
func (np *npmProject) RequiredExternalTools(context.Context) []tools.ExternalTool {
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
) (*ServiceRestoreResult, error) {
	np.publisher.Send(ctx, messaging.NewMessage(ProgressMessageKind, "Installing NPM dependencies"))
	if err := np.cli.Install(ctx, serviceConfig.Path()); err != nil {
		return nil, err
	}

	return &ServiceRestoreResult{}, nil
}

// Builds the project executing the npm `build` script defined within the project package.json
func (np *npmProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	restoreOutput *ServiceRestoreResult,
) (*ServiceBuildResult, error) {
	// Exec custom `build` script if available
	// If `build`` script is not defined in the package.json the NPM script will NOT fail
	np.publisher.Send(ctx, messaging.NewMessage(ProgressMessageKind, "Running NPM build script"))
	if err := np.cli.RunScript(ctx, serviceConfig.Path(), "build"); err != nil {
		return nil, err
	}

	buildSource := serviceConfig.Path()

	if serviceConfig.OutputPath != "" {
		buildSource = filepath.Join(buildSource, serviceConfig.OutputPath)
	}

	return &ServiceBuildResult{
		Restore:         restoreOutput,
		BuildOutputPath: buildSource,
	}, nil
}

func (np *npmProject) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
) (*ServicePackageResult, error) {
	packageDest, err := os.MkdirTemp("", "azd")
	if err != nil {
		return nil, fmt.Errorf("creating package directory for %s: %w", serviceConfig.Name, err)
	}

	// Exec custom `package` script if available
	// If `package` script is not defined in the package.json the NPM script will NOT fail
	np.publisher.Send(ctx, messaging.NewMessage(ProgressMessageKind, "Running NPM package script"))

	// Long term this script we call should better align with our inner-loop scenarios
	// Keeping this defaulted to `build` will create confusion for users when we start to support
	// both local dev / debug builds and production bundled builds
	if err := np.cli.RunScript(ctx, serviceConfig.Path(), "build"); err != nil {
		return nil, err
	}

	// Copy directory rooted by dist to package root.
	packageSource := buildOutput.BuildOutputPath
	if packageSource == "" {
		packageSource = filepath.Join(serviceConfig.Path(), serviceConfig.OutputPath)
	}

	if entries, err := os.ReadDir(packageSource); err != nil || len(entries) == 0 {
		return nil, fmt.Errorf(
			//nolint:lll
			"package source '%s' is empty or does not exist. If your service has custom packaging requirements create an NPM script named 'build' within your package.json and ensure your package artifacts are written to the '%s' directory",
			packageSource,
			packageSource,
		)
	}

	np.publisher.Send(ctx, messaging.NewMessage(ProgressMessageKind, "Copying deployment package"))
	if err := buildForZip(
		packageSource,
		packageDest,
		buildForZipOptions{
			excludeConditions: []excludeDirEntryCondition{
				excludeNodeModules,
			},
		}); err != nil {
		return nil, fmt.Errorf("packaging for %s: %w", serviceConfig.Name, err)
	}

	if err := validatePackageOutput(packageDest); err != nil {
		return nil, err
	}

	return &ServicePackageResult{
		Build:       buildOutput,
		PackagePath: packageDest,
	}, nil
}

const cNodeModulesName = "node_modules"

func excludeNodeModules(path string, file os.FileInfo) bool {
	return file.IsDir() && file.Name() == cNodeModulesName
}

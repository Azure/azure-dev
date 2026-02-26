// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/node"
)

type nodeProject struct {
	env           *environment.Environment
	cli           node.Cli
	commandRunner exec.CommandRunner

	mu       sync.Mutex
	cliCache map[string]node.Cli
}

// NewNodeProject creates a new instance of a Node.js project framework service.
// It auto-detects whether the project uses npm, pnpm, or yarn.
func NewNodeProject(cli node.Cli, env *environment.Environment, commandRunner exec.CommandRunner) FrameworkService {
	return &nodeProject{
		env:           env,
		cli:           cli,
		commandRunner: commandRunner,
		cliCache:      make(map[string]node.Cli),
	}
}

// cliForService returns a Cli configured for the package manager detected in the service directory.
// If the service's azure.yaml config specifies a "packageManager" override, that value is used instead
// of auto-detection. The result is cached per service path to ensure consistent detection across
// operations and avoid redundant filesystem I/O.
func (np *nodeProject) cliForService(serviceConfig *ServiceConfig) (node.Cli, error) {
	np.mu.Lock()
	defer np.mu.Unlock()

	path := serviceConfig.Path()
	if cached, ok := np.cliCache[path]; ok {
		return cached, nil
	}

	// Check for explicit packageManager override in azure.yaml service config
	pm, err := packageManagerFromConfig(serviceConfig)
	if err != nil {
		return nil, err
	}
	if pm == "" {
		pm = node.DetectPackageManager(path)
	}

	var cli node.Cli
	if pm != np.cli.PackageManager() {
		cli = node.NewCliWithPackageManager(np.commandRunner, pm)
	} else {
		cli = np.cli
	}
	np.cliCache[path] = cli
	return cli, nil
}

// packageManagerFromConfig reads an optional "packageManager" override from the service's config
// section in azure.yaml. Returns empty string if not set. Returns error if set to an invalid value.
func packageManagerFromConfig(serviceConfig *ServiceConfig) (node.PackageManagerKind, error) {
	if serviceConfig.Config == nil {
		return "", nil
	}
	raw, ok := serviceConfig.Config["packageManager"]
	if !ok {
		return "", nil
	}
	val, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("invalid packageManager config: expected a string, got %T", raw)
	}
	switch val {
	case "npm":
		return node.PackageManagerNpm, nil
	case "pnpm":
		return node.PackageManagerPnpm, nil
	case "yarn":
		return node.PackageManagerYarn, nil
	default:
		return "", fmt.Errorf("invalid packageManager config value %q: must be npm, pnpm, or yarn", val)
	}
}

func (np *nodeProject) Requirements() FrameworkRequirements {
	return FrameworkRequirements{
		Package: FrameworkPackageRequirements{
			// Node.js package managers require a restore before running scripts
			RequireRestore: true,
			RequireBuild:   false,
		},
	}
}

// Gets the required external tools for the project
func (np *nodeProject) RequiredExternalTools(_ context.Context, serviceConfig *ServiceConfig) []tools.ExternalTool {
	cli, err := np.cliForService(serviceConfig)
	if err != nil {
		// Fall back to default CLI if config is invalid — error will surface during Restore/Build
		return []tools.ExternalTool{np.cli}
	}
	return []tools.ExternalTool{cli}
}

// Initializes the Node.js project
func (np *nodeProject) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

// Restores dependencies for the project using the detected package manager's install command
func (np *nodeProject) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServiceRestoreResult, error) {
	cli, err := np.cliForService(serviceConfig)
	if err != nil {
		return nil, err
	}
	pm := string(cli.PackageManager())

	// Skip install if dependencies are already up-to-date (lockfile hasn't changed)
	if node.IsDependenciesUpToDate(serviceConfig.Path(), cli.PackageManager()) {
		progress.SetProgress(NewServiceProgress(fmt.Sprintf("%s dependencies already up-to-date", pm)))
	} else {
		progress.SetProgress(NewServiceProgress(fmt.Sprintf("Installing %s dependencies", pm)))
		if err := cli.Install(ctx, serviceConfig.Path()); err != nil {
			return nil, err
		}
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
					"framework":    pm,
					"dependencies": "node_modules",
				},
			},
		},
	}, nil
}

// Builds the project executing the `build` script defined within the project package.json
func (np *nodeProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServiceBuildResult, error) {
	cli, err := np.cliForService(serviceConfig)
	if err != nil {
		return nil, err
	}
	pm := string(cli.PackageManager())
	// Exec custom `build` script if available
	// If `build` script is not defined in the package.json the script will NOT fail
	progress.SetProgress(NewServiceProgress(fmt.Sprintf("Running %s build script", pm)))
	if err := cli.RunScript(ctx, serviceConfig.Path(), "build"); err != nil {
		return nil, err
	}

	buildSource := serviceConfig.Path()

	if serviceConfig.OutputPath != "" {
		buildSource = filepath.Join(buildSource, serviceConfig.OutputPath)
	}

	// Create build artifact for build output
	return &ServiceBuildResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     buildSource,
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"buildSource": buildSource,
					"framework":   pm,
					"outputPath":  serviceConfig.OutputPath,
				},
			},
		},
	}, nil
}

func (np *nodeProject) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	cli, err := np.cliForService(serviceConfig)
	if err != nil {
		return nil, err
	}
	pm := string(cli.PackageManager())
	progress.SetProgress(NewServiceProgress(fmt.Sprintf("Running %s package script", pm)))

	// Long term this script we call should better align with our inner-loop scenarios
	// Keeping this defaulted to `build` will create confusion for users when we start to support
	// both local dev / debug builds and production bundled builds
	if err := cli.RunScript(ctx, serviceConfig.Path(), "build"); err != nil {
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
				"a script named 'build' within your package.json and ensure your package artifacts are written to "+
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

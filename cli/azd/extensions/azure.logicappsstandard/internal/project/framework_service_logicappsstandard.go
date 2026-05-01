// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// Ensure LogicAppsStandardFrameworkServiceProvider implements FrameworkServiceProvider interface
var _ azdext.FrameworkServiceProvider = &LogicAppsStandardFrameworkServiceProvider{}

// LogicAppsStandardFrameworkServiceProvider introduces the custom language 'logicappsstandard',
// which makes it possible to package Logic Apps Standard projects, including those with custom code components.
type LogicAppsStandardFrameworkServiceProvider struct {
	serviceConfig *azdext.ServiceConfig
}

func NewLogicAppsStandardFrameworkServiceProvider() azdext.FrameworkServiceProvider {
	return &LogicAppsStandardFrameworkServiceProvider{}
}

// Initialize initializes the framework service provider with service configuration
func (p *LogicAppsStandardFrameworkServiceProvider) Initialize(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
) error {
	p.serviceConfig = serviceConfig

	if hasCustomCodeProjectConfigured(serviceConfig) {
		csProjPath, err := p.resolveCustomCodeProjectPath(serviceConfig)
		if err != nil {
			return err
		}
		projectInfo, err := os.Stat(csProjPath)
		if err != nil {
			return fmt.Errorf("custom code project not found at '%s': %w", csProjPath, err)
		}
		if projectInfo.IsDir() {
			return fmt.Errorf("custom code project path '%s' must point to a file", csProjPath)
		}
	}

	return nil
}

// Returns dotnet as required external tool if a custom code project is configured
func (p *LogicAppsStandardFrameworkServiceProvider) RequiredExternalTools(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
) ([]*azdext.ExternalTool, error) {
	if hasCustomCodeProjectConfigured(serviceConfig) {
		return []*azdext.ExternalTool{
			{
				Name:       "dotnet",
				InstallUrl: "https://dotnet.microsoft.com/download",
			},
		}, nil
	}
	return nil, nil
}

// Requirements returns the framework requirements (whether restore/build are needed)
func (p *LogicAppsStandardFrameworkServiceProvider) Requirements() (*azdext.FrameworkRequirements, error) {
	hasCustomCodeProject := p.serviceConfig != nil && hasCustomCodeProjectConfigured(p.serviceConfig)
	return &azdext.FrameworkRequirements{
		Package: &azdext.FrameworkPackageRequirements{
			RequireRestore: hasCustomCodeProject,
			RequireBuild:   hasCustomCodeProject,
		},
	}, nil
}

// Restores the dependencies for the custom code project if specified in the service configuration.
func (p *LogicAppsStandardFrameworkServiceProvider) Restore(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServiceRestoreResult, error) {
	if hasCustomCodeProjectConfigured(serviceConfig) {
		progress("Restoring .NET project dependencies")
		csProjPath, err := p.resolveCustomCodeProjectPath(serviceConfig)
		if err != nil {
			return nil, err
		}
		if err := runDotNet(ctx, "restore", csProjPath); err != nil {
			return nil, fmt.Errorf("restoring custom code project '%s': %w", csProjPath, err)
		}
	}
	return &azdext.ServiceRestoreResult{}, nil
}

// Builds the custom code project if specified in the service configuration.
func (p *LogicAppsStandardFrameworkServiceProvider) Build(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServiceBuildResult, error) {
	if hasCustomCodeProjectConfigured(serviceConfig) {
		progress("Building .NET project")
		csProjPath, err := p.resolveCustomCodeProjectPath(serviceConfig)
		if err != nil {
			return nil, err
		}
		if err := runDotNet(ctx, "build", csProjPath, "--configuration", "Release"); err != nil {
			return nil, fmt.Errorf("building custom code project '%s': %w", csProjPath, err)
		}
	}
	return &azdext.ServiceBuildResult{}, nil
}

// Packages the Logic Apps Standard project, including any custom code components, into a deployable artifact.
func (p *LogicAppsStandardFrameworkServiceProvider) Package(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	progress("Creating Logic Apps Standard deployment package")

	projectDir, err := azdext.GetProjectDir()
	if err != nil {
		return nil, fmt.Errorf("getting project directory: %w", err)
	}

	// Determine the absolute path to the Logic Apps Standard project based on the project directory
	// and the relative path specified in the service configuration.
	packagePath := filepath.Join(projectDir, serviceConfig.RelativePath)

	// If an output path (dist) is specified, append it to the package path
	// This allows for subdirectories like "Workflows" to be the root of the zip
	if serviceConfig.OutputPath != "" {
		packagePath = filepath.Join(packagePath, serviceConfig.OutputPath)
	}

	// Return a DIRECTORY artifact pointing to the project root.
	// azd's packaging pipeline will handle creating the zip archive from this directory.
	// By specifying an absolute path, azd will use the host specific ignore file (e.g. .funcignore) for exclusions.
	return &azdext.ServicePackageResult{
		Artifacts: []*azdext.Artifact{
			{
				Kind:         azdext.ArtifactKind_ARTIFACT_KIND_DIRECTORY,
				Location:     packagePath,
				LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
			},
		},
	}, nil
}

func (p *LogicAppsStandardFrameworkServiceProvider) resolveCustomCodeProjectPath(
	serviceConfig *azdext.ServiceConfig,
) (string, error) {
	projectDir, err := azdext.GetProjectDir()
	if err != nil {
		return "", fmt.Errorf("getting project directory: %w", err)
	}

	return filepath.Join(
		projectDir,
		serviceConfig.RelativePath,
		getAdditionalProperty(serviceConfig, "customCodeProject"),
	), nil
}

// getAdditionalProperty retrieves a custom property from the service configuration's additional properties.
func getAdditionalProperty(serviceConfig *azdext.ServiceConfig, key string) string {
	props := serviceConfig.GetAdditionalProperties()
	if props == nil {
		return ""
	}
	if v, ok := props.Fields[key]; ok {
		return v.GetStringValue()
	}
	return ""
}

func hasCustomCodeProjectConfigured(serviceConfig *azdext.ServiceConfig) bool {
	return getAdditionalProperty(serviceConfig, "customCodeProject") != ""
}

// runDotNet executes the dotnet CLI with the given arguments, forwarding output to stdout/stderr.
func runDotNet(ctx context.Context, args ...string) error {
	cmd := azdext.ExecCommand(ctx, "dotnet", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

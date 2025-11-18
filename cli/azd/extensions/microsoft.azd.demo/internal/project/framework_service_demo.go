// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// Ensure DemoFrameworkServiceProvider implements FrameworkServiceProvider interface
var _ azdext.FrameworkServiceProvider = &DemoFrameworkServiceProvider{}

// DemoFrameworkServiceProvider is a demonstration implementation of a framework service provider
// This shows how to implement support for a custom language/framework that isn't built into azd
type DemoFrameworkServiceProvider struct {
	azdClient     *azdext.AzdClient
	serviceConfig *azdext.ServiceConfig
}

// NewDemoFrameworkServiceProvider creates a new DemoFrameworkServiceProvider instance
func NewDemoFrameworkServiceProvider(azdClient *azdext.AzdClient) azdext.FrameworkServiceProvider {
	return &DemoFrameworkServiceProvider{
		azdClient: azdClient,
	}
}

// Initialize initializes the framework service provider with service configuration
func (p *DemoFrameworkServiceProvider) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
	if serviceConfig != nil {
		fmt.Printf("Demo framework service initializing for service: %s (language: rust)\n", serviceConfig.GetName())
	}
	p.serviceConfig = serviceConfig
	return nil
}

// RequiredExternalTools returns the external tools required by this framework
func (p *DemoFrameworkServiceProvider) RequiredExternalTools(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
) ([]*azdext.ExternalTool, error) {
	// For this demo, we'll say that Rust requires cargo and rustc
	tools := []*azdext.ExternalTool{
		{
			Name:       "cargo",
			InstallUrl: "https://rustup.rs/",
		},
		{
			Name:       "rustc",
			InstallUrl: "https://rustup.rs/",
		},
	}

	return tools, nil
}

// Requirements returns the framework requirements (whether restore/build are needed)
func (p *DemoFrameworkServiceProvider) Requirements() (*azdext.FrameworkRequirements, error) {
	return &azdext.FrameworkRequirements{
		Package: &azdext.FrameworkPackageRequirements{
			RequireRestore: true, // Rust needs cargo build which includes dependency resolution
			RequireBuild:   true, // Rust needs compilation
		},
	}, nil
}

// Restore performs package restore/install for dependencies
func (p *DemoFrameworkServiceProvider) Restore(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServiceRestoreResult, error) {
	progress("Installing Rust dependencies")
	time.Sleep(500 * time.Millisecond)

	progress("Checking Cargo.toml")
	time.Sleep(300 * time.Millisecond)

	progress("Downloading crates")
	time.Sleep(800 * time.Millisecond)

	fmt.Printf("\nRust dependencies restored for: %s\n", serviceConfig.GetName())

	// Create restore artifact for dependencies
	restoreArtifact := &azdext.Artifact{
		Kind:         azdext.ArtifactKind_ARTIFACT_KIND_DIRECTORY,
		Location:     "./target/debug/deps",
		LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
		Metadata: map[string]string{
			"timestamp":     time.Now().Format(time.RFC3339),
			"dependencyMgr": "cargo",
			"rustVersion":   "1.70.0",
			"framework":     "rust",
		},
	}

	return &azdext.ServiceRestoreResult{
		Artifacts: []*azdext.Artifact{restoreArtifact},
	}, nil
}

// Build compiles the application
func (p *DemoFrameworkServiceProvider) Build(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServiceBuildResult, error) {
	progress("Compiling Rust project")
	time.Sleep(2 * time.Second)

	progress("Linking dependencies")
	time.Sleep(2 * time.Second)

	progress("Generating binary")
	time.Sleep(3 * time.Second)

	fmt.Printf("\nRust project built: %s\n", serviceConfig.GetName())

	buildArtifacts := []*azdext.Artifact{
		{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_DIRECTORY,
			Location:     "target/release/" + serviceConfig.GetName(),
			LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
			Metadata: map[string]string{
				"timestamp": time.Now().Format(time.RFC3339),
				"buildMode": "release",
				"target":    "x86_64-unknown-linux-gnu",
				"type":      "binary", // Store the specific type in metadata
			},
		},
	}

	return &azdext.ServiceBuildResult{
		Artifacts: buildArtifacts,
	}, nil
}

// Package performs packaging (creating deployable artifacts)
func (p *DemoFrameworkServiceProvider) Package(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	progress("Creating Rust deployment package")
	time.Sleep(600 * time.Millisecond)

	progress("Bundling binary and assets")
	time.Sleep(400 * time.Millisecond)

	packagePath := fmt.Sprintf("rust-app-%s.tar.gz", serviceConfig.GetName())
	fmt.Printf("\nRust package created: %s\n", packagePath)

	packageArtifacts := []*azdext.Artifact{
		{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_ARCHIVE,
			Location:     packagePath,
			LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
			Metadata: map[string]string{
				"timestamp":      time.Now().Format(time.RFC3339),
				"packageType":    "tar.gz",
				"binaryIncluded": "true",
				"size":           "15.2MB",
			},
		},
	}

	return &azdext.ServicePackageResult{
		Artifacts: packageArtifacts,
	}, nil
}

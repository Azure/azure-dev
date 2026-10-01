// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/golang"
	"github.com/otiai10/copy"
)

const (
	// goBinaryName is the compiled binary name for the Azure Functions Go worker on Linux.
	//
	// The binary name ("app"), Linux/amd64 target, and CGO being disabled are dictated by
	// the Flex Consumption Go worker contract: the platform ships a proxy that execs this
	// binary by name. See the worker spec for the authoritative source of truth:
	// https://github.com/Azure/azure-functions-golang-worker
	// A future change (e.g. a different worker name, or arm64 support) should update both
	// this constant and the build target env vars in Build below.
	goBinaryName = "app"
)

type goProject struct {
	env   *environment.Environment
	goCli *golang.Cli
}

// NewGoProject creates a new instance of a Go project framework service.
func NewGoProject(
	env *environment.Environment,
	goCli *golang.Cli,
) FrameworkService {
	return &goProject{
		env:   env,
		goCli: goCli,
	}
}

func (gp *goProject) Requirements() FrameworkRequirements {
	return FrameworkRequirements{
		Package: FrameworkPackageRequirements{
			RequireRestore: true,
			RequireBuild:   true,
		},
	}
}

// RequiredExternalTools returns the Go CLI as a required tool.
func (gp *goProject) RequiredExternalTools(
	_ context.Context,
	_ *ServiceConfig,
) []tools.ExternalTool {
	return []tools.ExternalTool{gp.goCli}
}

// Initialize is a no-op for Go projects.
func (gp *goProject) Initialize(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) error {
	return nil
}

// Restore downloads Go module dependencies.
func (gp *goProject) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServiceRestoreResult, error) {
	progress.SetProgress(NewServiceProgress("Downloading Go modules"))
	if err := gp.goCli.ModDownload(ctx, serviceConfig.Path(), gp.env.Environ()); err != nil {
		return nil, fmt.Errorf("restoring Go dependencies: %w", err)
	}

	return &ServiceRestoreResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     serviceConfig.Path(),
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"projectPath": serviceConfig.Path(),
					"framework":   "go",
				},
			},
		},
	}, nil
}

// Build compiles the Go project, cross-compiling for linux/amd64.
func (gp *goProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServiceBuildResult, error) {
	progress.SetProgress(NewServiceProgress("Compiling Go project"))

	buildDir, err := os.MkdirTemp("", "azd-go-build")
	if err != nil {
		return nil, fmt.Errorf("creating build directory: %w", err)
	}

	outputPath := filepath.Join(buildDir, goBinaryName)

	// Cross-compile for linux/amd64 with CGO disabled, as required by the Flex
	// Consumption Go worker contract (see goBinaryName above for the spec link).
	buildEnv := append(
		gp.env.Environ(),
		"GOOS=linux",
		"GOARCH=amd64",
		"CGO_ENABLED=0",
	)

	if err := gp.goCli.Build(
		ctx, serviceConfig.Path(), outputPath, buildEnv,
	); err != nil {
		os.RemoveAll(buildDir)
		return nil, fmt.Errorf("compiling Go project: %w", err)
	}

	return &ServiceBuildResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     buildDir,
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"buildPath":  buildDir,
					"binaryPath": outputPath,
					"framework":  "go",
					"targetOS":   "linux",
					"targetArch": "amd64",
					"buildOS":    runtime.GOOS,
					"buildArch":  runtime.GOARCH,
				},
			},
		},
	}, nil
}

// Package stages the compiled binary and host.json into a deployment directory
// suitable for Azure Functions zip deploy.
// On Flex Consumption with runtime 'go', the platform provides the worker.config.json
// and proxy binary — the deployment package only needs the app binary and host.json.
func (gp *goProject) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	progress.SetProgress(NewServiceProgress("Staging Go Functions deployment"))

	// Resolve the compiled binary path from the build artifact. We own the Build
	// step (see Build above), which always records an absolute binaryPath pointing
	// at <buildDir>/app, so we can trust the metadata directly without sanitizing
	// against path traversal. Fall back to <buildDir>/app if the metadata is absent.
	artifact, found := serviceContext.Build.FindFirst(WithKind(ArtifactKindDirectory))
	if !found {
		return nil, fmt.Errorf("no build output found in service context")
	}
	binaryPath := artifact.Metadata["binaryPath"]
	if binaryPath == "" {
		binaryPath = filepath.Join(artifact.Location, goBinaryName)
	}

	packageDir, err := os.MkdirTemp("", "azd-go-package")
	if err != nil {
		return nil, fmt.Errorf("creating package directory: %w", err)
	}

	// Copy compiled binary and ensure execute permission is set.
	// On Windows, os.Chmod is a no-op for Unix execute bits; the binary is instead
	// flagged executable at zip time via rzip.WithExecutableMatcher (see createDeployableZip).
	progress.SetProgress(NewServiceProgress("Copying compiled binary"))
	destBinaryPath := filepath.Join(packageDir, filepath.Base(binaryPath))
	if err := copy.Copy(binaryPath, destBinaryPath); err != nil {
		return nil, fmt.Errorf("copying Go binary: %w", err)
	}
	if err := os.Chmod(destBinaryPath, 0755); err != nil {
		return nil, fmt.Errorf("setting binary permissions: %w", err)
	}

	// Copy host.json from user project (required for Azure Functions deployment)
	hostJSONSrc := filepath.Join(serviceConfig.Path(), "host.json")
	if _, err := os.Stat(hostJSONSrc); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf(
				"host.json not found at %q: Azure Functions requires a host.json file in the project directory",
				hostJSONSrc,
			)
		}
		return nil, fmt.Errorf("checking host.json at %q: %w", hostJSONSrc, err)
	}
	if err := copy.Copy(
		hostJSONSrc, filepath.Join(packageDir, "host.json"),
	); err != nil {
		return nil, fmt.Errorf("copying host.json: %w", err)
	}

	return &ServicePackageResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     packageDir,
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"packagePath": packageDir,
					"framework":   "go",
					"host":        "azure-function",
				},
			},
		},
	}, nil
}

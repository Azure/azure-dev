// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package packaging

import (
	"context"

	"azurespringboot/internal/exterrors"
	"azurespringboot/internal/springmodel"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

const (
	// SpringDockerfile is the generated Eclipse Temurin 17 container recipe.
	SpringDockerfile = springDockerfile
	// StagingDirectoryName is the temporary staging directory prefix.
	StagingDirectoryName = stagingDirectoryName
	// StagedJarName is the normalized executable JAR filename inside the staging context.
	StagedJarName = stagedJarName
	// SpringHost is the canonical service-target host name.
	SpringHost = springmodel.SpringHost
)

func validationError(code, message, suggestion string) error {
	return exterrors.Validation(code, message, suggestion)
}

func nonDisclosureError(err error) error {
	return exterrors.NonDisclosureError(err)
}

// StagedContainerContext wraps the isolated temporary build context for a Spring Boot image.
type StagedContainerContext = stagedContainerContext

// Directory returns the staged container build context directory.
func (c *stagedContainerContext) Directory() string {
	return c.directory
}

// Cleanup removes the staged container build context after verifying file integrity.
func (c *stagedContainerContext) Cleanup() error {
	return c.cleanup()
}

// RuntimeImageBuilder builds a local container image from a StagedContainerContext.
type RuntimeImageBuilder = runtimeImageBuilder

// NewRuntimeImageBuilder creates a Docker-backed RuntimeImageBuilder.
func NewRuntimeImageBuilder(runner azdexec.CommandRunner) RuntimeImageBuilder {
	return newRuntimeImageBuilder(runner)
}

// ServicePath resolves and validates a contained Maven Spring Boot service directory.
func ServicePath(project *azdext.ProjectConfig, service *azdext.ServiceConfig) (string, error) {
	return servicePath(project, service)
}

// FindSpringJar resolves and validates the packaged Maven app.jar artifact.
func FindSpringJar(serviceContext *azdext.ServiceContext, root string) (string, error) {
	return findSpringJar(serviceContext, root)
}

// PrepareContainerContext stages the packaged JAR and generated Java 17 Dockerfile in a fresh temp directory.
func PrepareContainerContext(
	ctx context.Context,
	project *azdext.ProjectConfig,
	service *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
) (*StagedContainerContext, error) {
	return prepareContainerContext(ctx, project, service, serviceContext)
}

// ValidateRawServiceConfiguration rejects unsupported raw YAML keys on a Spring service map.
func ValidateRawServiceConfiguration(serviceName string, service map[string]any) error {
	return validateRawServiceConfiguration(serviceName, service)
}

// ValidateServiceConfiguration rejects unsupported authored top-level properties on a Spring service.
func ValidateServiceConfiguration(service *azdext.ServiceConfig) error {
	return validateServiceConfiguration(service)
}

// ValidateSpringBuild validates local Maven packaging inputs without modifying authored files.
func ValidateSpringBuild(project *azdext.ProjectConfig, service *azdext.ServiceConfig) error {
	return validateSpringBuild(project, service)
}

// PrepareSpringBuild validates local Maven packaging inputs prior to building.
func PrepareSpringBuild(project *azdext.ProjectConfig, service *azdext.ServiceConfig) error {
	return prepareSpringBuild(project, service)
}

// NativeServiceContext converts an extension ServiceContext to azd's native project.ServiceContext.
func NativeServiceContext(serviceContext *azdext.ServiceContext) *project.ServiceContext {
	return nativeServiceContext(serviceContext)
}

// ExtensionArtifacts converts native project artifacts to extension gRPC artifacts.
func ExtensionArtifacts(artifacts project.ArtifactCollection) []*azdext.Artifact {
	return extensionArtifacts(artifacts)
}

// RunWithProgress bridges async.Progress updates to a gRPC ProgressFunc.
func RunWithProgress[T any](
	ctx context.Context,
	report grpcbroker.ProgressFunc,
	run func(*async.Progress[project.ServiceProgress]) (T, error),
) (T, error) {
	return runWithProgress(ctx, report, run)
}

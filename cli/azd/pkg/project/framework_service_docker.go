// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/messaging"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
)

type DockerProjectOptions struct {
	Path     string           `json:"path"`
	Context  string           `json:"context"`
	Platform string           `json:"platform"`
	Tag      ExpandableString `json:"tag"`
}

type dockerBuildResult struct {
	ImageId   string `json:"imageId"`
	ImageName string `json:"imageName"`
}

func (dbr *dockerBuildResult) ToString(currentIndentation string) string {
	currentIndentation += "  "

	lines := []string{
		fmt.Sprintf("%s- Image ID: %s", currentIndentation, output.WithLinkFormat(dbr.ImageId)),
		fmt.Sprintf("%s- Image Name: %s", currentIndentation, output.WithLinkFormat(dbr.ImageName)),
	}

	return strings.Join(lines, "\n")
}

func (dbr *dockerBuildResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(*dbr)
}

type dockerPackageResult struct {
	ImageHash string `json:"imageHash"`
	ImageTag  string `json:"imageTag"`
}

func (dpr *dockerPackageResult) ToString(currentIndentation string) string {
	currentIndentation += "  "
	lines := []string{
		fmt.Sprintf("%s- Image Hash: %s", currentIndentation, output.WithLinkFormat(dpr.ImageHash)),
		fmt.Sprintf("%s- Image Tag: %s", currentIndentation, output.WithLinkFormat(dpr.ImageTag)),
	}

	return strings.Join(lines, "\n")
}

func (dpr *dockerPackageResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(*dpr)
}

type dockerProject struct {
	env             *environment.Environment
	docker          docker.Docker
	framework       FrameworkService
	containerHelper *ContainerHelper
	publisher       messaging.Publisher
}

// NewDockerProject creates a new instance of a Azd project that
// leverages docker for building
func NewDockerProject(
	env *environment.Environment,
	docker docker.Docker,
	containerHelper *ContainerHelper,
	publisher messaging.Publisher,
) CompositeFrameworkService {
	return &dockerProject{
		env:             env,
		docker:          docker,
		containerHelper: containerHelper,
		publisher:       publisher,
	}
}

func (p *dockerProject) Requirements() FrameworkRequirements {
	return FrameworkRequirements{
		Package: FrameworkPackageRequirements{
			RequireRestore: false,
			// Docker project needs to build a container image
			RequireBuild: true,
		},
	}
}

// Gets the required external tools for the project
func (p *dockerProject) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{p.docker}
}

// Initializes the docker project
func (p *dockerProject) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return p.framework.Initialize(ctx, serviceConfig)
}

// Sets the inner framework service used for restore and build command
func (p *dockerProject) SetSource(inner FrameworkService) {
	p.framework = inner
}

// Restores the dependencies for the docker project
func (p *dockerProject) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) (*ServiceRestoreResult, error) {
	// When the program runs the restore actions for the underlying project (containerapp),
	// the dependencies are installed locally
	return p.framework.Restore(ctx, serviceConfig)
}

// Builds the docker project based on the docker options specified within the Service configuration
func (p *dockerProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	restoreOutput *ServiceRestoreResult,
) (*ServiceBuildResult, error) {
	dockerOptions := getDockerOptionsWithDefaults(serviceConfig.Docker)

	log.Printf(
		"building image for service %s, cwd: %s, path: %s, context: %s)",
		serviceConfig.Name,
		serviceConfig.Path(),
		dockerOptions.Path,
		dockerOptions.Context,
	)

	imageName := fmt.Sprintf(
		"%s-%s",
		strings.ToLower(serviceConfig.Project.Name),
		strings.ToLower(serviceConfig.Name),
	)

	// Build the container
	p.publisher.Send(ctx, messaging.NewMessage(ProgressMessageKind, "Building Docker image"))
	imageId, err := p.docker.Build(
		ctx,
		serviceConfig.Path(),
		dockerOptions.Path,
		dockerOptions.Platform,
		dockerOptions.Context,
		imageName,
	)
	if err != nil {
		return nil, fmt.Errorf("building container: %s at %s: %w", serviceConfig.Name, dockerOptions.Context, err)
	}

	log.Printf("built image %s for %s", imageId, serviceConfig.Name)
	return &ServiceBuildResult{
		Restore:         restoreOutput,
		BuildOutputPath: imageId,
		Details: &dockerBuildResult{
			ImageId:   imageId,
			ImageName: imageName,
		},
	}, nil
}

func (p *dockerProject) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
) (*ServicePackageResult, error) {
	imageId := buildOutput.BuildOutputPath
	if imageId == "" {
		return nil, errors.New("missing container image id from build output")
	}

	localTag, err := p.containerHelper.LocalImageTag(ctx, serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("generating local image tag: %w", err)
	}

	// Tag image.
	log.Printf("tagging image %s as %s", imageId, localTag)
	p.publisher.Send(ctx, messaging.NewMessage(ProgressMessageKind, "Tagging Docker image"))
	if err := p.docker.Tag(ctx, serviceConfig.Path(), imageId, localTag); err != nil {
		return nil, fmt.Errorf("tagging image: %w", err)
	}

	return &ServicePackageResult{
		Build:       buildOutput,
		PackagePath: localTag,
		Details: &dockerPackageResult{
			ImageHash: imageId,
			ImageTag:  localTag,
		},
	}, nil
}

func getDockerOptionsWithDefaults(options DockerProjectOptions) DockerProjectOptions {
	if options.Path == "" {
		options.Path = "./Dockerfile"
	}

	if options.Platform == "" {
		options.Platform = docker.DefaultPlatform
	}

	if options.Context == "" {
		options.Context = "."
	}

	return options
}

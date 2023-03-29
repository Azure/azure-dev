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

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
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
	lines := []string{
		fmt.Sprintf("%s- Image ID: %s", currentIndentation, output.WithLinkFormat(dbr.ImageId)),
		fmt.Sprintf("%s- Image Name: %s", currentIndentation, output.WithLinkFormat(dbr.ImageName)),
	}

	return strings.Join(lines, "\n")
}

func (dbr *dockerBuildResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(dbr)
}

type dockerPackageResult struct {
	ImageHash string `json:"imageHash"`
	ImageTag  string `json:"imageTag"`
}

func (dpr *dockerPackageResult) ToString(currentIndentation string) string {
	lines := []string{
		fmt.Sprintf("%s- Image Hash: %s", currentIndentation, output.WithLinkFormat(dpr.ImageHash)),
		fmt.Sprintf("%s- Image Tag: %s", currentIndentation, output.WithLinkFormat(dpr.ImageTag)),
	}

	return strings.Join(lines, "\n")
}

func (dpr *dockerPackageResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(dpr)
}

type dockerProject struct {
	env             *environment.Environment
	docker          docker.Docker
	framework       FrameworkService
	containerHelper *ContainerHelper
}

// NewDockerProject creates a new instance of a Azd project that
// leverages docker for building
func NewDockerProject(
	env *environment.Environment,
	docker docker.Docker,
	containerHelper *ContainerHelper,
) CompositeFrameworkService {
	return &dockerProject{
		env:             env,
		docker:          docker,
		containerHelper: containerHelper,
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
) *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress] {
	// When the program runs the restore actions for the underlying project (containerapp),
	// the dependencies are installed locally
	return p.framework.Restore(ctx, serviceConfig)
}

// Builds the docker project based on the docker options specified within the Service configuration
func (p *dockerProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	restoreOutput *ServiceRestoreResult,
) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceBuildResult, ServiceProgress]) {
			dockerOptions := getDockerOptionsWithDefaults(serviceConfig.Docker)

			log.Printf(
				"building image for service %s, cwd: %s, path: %s, context: %s)",
				serviceConfig.Name,
				serviceConfig.Path(),
				dockerOptions.Path,
				dockerOptions.Context,
			)

			imageName := fmt.Sprintf("%s-%s", serviceConfig.Project.Name, serviceConfig.Name)

			// Build the container
			task.SetProgress(NewServiceProgress("Building docker image"))
			imageId, err := p.docker.Build(
				ctx,
				serviceConfig.Path(),
				dockerOptions.Path,
				dockerOptions.Platform,
				dockerOptions.Context,
				imageName,
			)
			if err != nil {
				task.SetError(fmt.Errorf("building container: %s at %s: %w", serviceConfig.Name, dockerOptions.Context, err))
				return
			}

			log.Printf("built image %s for %s", imageId, serviceConfig.Name)
			task.SetResult(&ServiceBuildResult{
				Restore:         restoreOutput,
				BuildOutputPath: imageId,
				Details: &dockerBuildResult{
					ImageId:   imageId,
					ImageName: imageName,
				},
			})
		},
	)
}

func (p *dockerProject) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
			imageId := buildOutput.BuildOutputPath
			if imageId == "" {
				task.SetError(errors.New("missing container image id from build output"))
				return
			}

			localTag, err := p.containerHelper.LocalImageTag(ctx, serviceConfig)
			if err != nil {
				task.SetError(fmt.Errorf("generating local image tag: %w", err))
				return
			}

			// Tag image.
			log.Printf("tagging image %s as %s", imageId, localTag)
			task.SetProgress(NewServiceProgress("Tagging docker image"))
			if err := p.docker.Tag(ctx, serviceConfig.Path(), imageId, localTag); err != nil {
				task.SetError(fmt.Errorf("tagging image: %w", err))
				return
			}

			task.SetResult(&ServicePackageResult{
				Build:       buildOutput,
				PackagePath: localTag,
				Details: &dockerPackageResult{
					ImageHash: imageId,
					ImageTag:  localTag,
				},
			})
		},
	)
}

func getDockerOptionsWithDefaults(options DockerProjectOptions) DockerProjectOptions {
	if options.Path == "" {
		options.Path = "./Dockerfile"
	}

	if options.Platform == "" {
		options.Platform = "amd64"
	}

	if options.Context == "" {
		options.Context = "."
	}

	return options
}

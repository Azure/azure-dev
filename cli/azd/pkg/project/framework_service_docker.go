// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/benbjohnson/clock"
)

type DockerProjectOptions struct {
	Path     string           `json:"path"`
	Context  string           `json:"context"`
	Platform string           `json:"platform"`
	Tag      ExpandableString `json:"tag"`
}

type dockerPackageResult struct {
	ImageTag    string
	LoginServer string
}

type dockerProject struct {
	env       *environment.Environment
	docker    docker.Docker
	framework FrameworkService
	clock     clock.Clock
}

// NewDockerProject creates a new instance of a Azd project that
// leverages docker for building
func NewDockerProject(
	env *environment.Environment,
	docker docker.Docker,
	clock clock.Clock,
) CompositeFrameworkService {
	return &dockerProject{
		env:    env,
		docker: docker,
		clock:  clock,
	}
}

// Gets the required external tools for the project
func (p *dockerProject) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{p.docker}
}

// Initializes the docker project
func (p *dockerProject) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
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

			// Build the container
			task.SetProgress(NewServiceProgress("Building docker image"))
			imageId, err := p.docker.Build(
				ctx,
				serviceConfig.Path(),
				dockerOptions.Path,
				dockerOptions.Platform,
				dockerOptions.Context,
			)
			if err != nil {
				task.SetError(fmt.Errorf("building container: %s at %s: %w", serviceConfig.Name, dockerOptions.Context, err))
				return
			}

			log.Printf("built image %s for %s", imageId, serviceConfig.Name)
			task.SetResult(&ServiceBuildResult{
				Restore:         restoreOutput,
				BuildOutputPath: imageId,
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
			loginServer, has := p.env.Values[environment.ContainerRegistryEndpointEnvVarName]
			if !has {
				task.SetError(fmt.Errorf(
					"could not determine container registry endpoint, ensure %s is set as an output of your infrastructure",
					environment.ContainerRegistryEndpointEnvVarName,
				))
				return
			}

			imageId := buildOutput.BuildOutputPath
			if imageId == "" {
				task.SetError(errors.New("missing container image id from build output"))
				return
			}

			imageTag, err := p.generateImageTag(serviceConfig)
			if err != nil {
				task.SetError(fmt.Errorf("generating image tag: %w", err))
				return
			}

			fullTag := fmt.Sprintf(
				"%s/%s",
				loginServer,
				imageTag,
			)

			// Tag image.
			log.Printf("tagging image %s as %s", imageId, fullTag)
			task.SetProgress(NewServiceProgress("Tagging docker image"))
			if err := p.docker.Tag(ctx, serviceConfig.Path(), imageId, fullTag); err != nil {
				task.SetError(fmt.Errorf("tagging image: %w", err))
				return
			}

			// Save the name of the image we pushed into the environment with a well known key.
			log.Printf("writing image name to environment")
			p.env.SetServiceProperty(serviceConfig.Name, "IMAGE_NAME", fullTag)

			if err := p.env.Save(); err != nil {
				task.SetError(fmt.Errorf("saving image name to environment: %w", err))
				return
			}

			task.SetResult(&ServicePackageResult{
				Build:       buildOutput,
				PackagePath: fullTag,
				Details: &dockerPackageResult{
					ImageTag:    fullTag,
					LoginServer: loginServer,
				},
			})
		},
	)
}

func (p *dockerProject) generateImageTag(serviceConfig *ServiceConfig) (string, error) {
	configuredTag, err := serviceConfig.Docker.Tag.Envsubst(p.env.Getenv)
	if err != nil {
		return "", err
	}

	if configuredTag != "" {
		return configuredTag, nil
	}

	return fmt.Sprintf("%s/%s-%s:azd-deploy-%d",
		strings.ToLower(serviceConfig.Project.Name),
		strings.ToLower(serviceConfig.Name),
		strings.ToLower(p.env.GetEnvName()),
		p.clock.Now().Unix(),
	), nil
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

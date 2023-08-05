// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/pack"
	"github.com/benbjohnson/clock"
)

const BuilderImage = "mcr.microsoft.com/oryx/builder:cbl-mariner-2.0"

type DockerProjectOptions struct {
	Path      string           `json:"path,omitempty"`
	Context   string           `json:"context,omitempty"`
	Platform  string           `json:"platform,omitempty"`
	Tag       ExpandableString `json:"tag,omitempty"`
	BuildArgs []string         `json:"buildArgs,omitempty"`
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
	return json.Marshal(*dbr)
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
	return json.Marshal(*dpr)
}

type dockerProject struct {
	env             *environment.Environment
	docker          docker.Docker
	pack            pack.PackCli
	framework       FrameworkService
	containerHelper *ContainerHelper
	console         input.Console
	clock           clock.Clock
}

// NewDockerProject creates a new instance of a Azd project that
// leverages docker for building
func NewDockerProject(
	env *environment.Environment,
	docker docker.Docker,
	pack pack.PackCli,
	containerHelper *ContainerHelper,
	console input.Console,
	clock clock.Clock,
) CompositeFrameworkService {
	return &dockerProject{
		env:             env,
		docker:          docker,
		pack:            pack,
		containerHelper: containerHelper,
		console:         console,
		clock:           clock,
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

			buildArgs := []string{}
			for _, arg := range dockerOptions.BuildArgs {
				buildArgs = append(buildArgs, exec.RedactSensitiveData(arg))
			}

			log.Printf(
				"building image for service %s, cwd: %s, path: %s, context: %s, buildArgs: %s)",
				serviceConfig.Name,
				serviceConfig.Path(),
				dockerOptions.Path,
				dockerOptions.Context,
				buildArgs,
			)

			imageName := fmt.Sprintf(
				"%s-%s",
				strings.ToLower(serviceConfig.Project.Name),
				strings.ToLower(serviceConfig.Name),
			)

			// Build the container

			path := filepath.Join(serviceConfig.Path(), dockerOptions.Path)
			_, err := os.Stat(path)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				task.SetError(fmt.Errorf("reading dockerfile: %w", err))
				return
			}

			if errors.Is(err, os.ErrNotExist) {
				task.SetProgress(NewServiceProgress("Building Docker image from source"))
				buildResult, err := p.packBuild(ctx, serviceConfig, dockerOptions, imageName)
				if err != nil {
					task.SetError(err)
					return
				}

				buildResult.Restore = restoreOutput
				task.SetResult(buildResult)
				return
			}

			task.SetProgress(NewServiceProgress("Building Docker image"))
			previewerWriter := p.console.ShowPreviewer(ctx,
				&input.ShowPreviewerOptions{
					Prefix:       "  ",
					MaxLineCount: 8,
					Title:        "Docker Output",
				})
			imageId, err := p.docker.Build(
				ctx,
				serviceConfig.Path(),
				dockerOptions.Path,
				dockerOptions.Platform,
				dockerOptions.Context,
				imageName,
				dockerOptions.BuildArgs,
				previewerWriter,
			)
			p.console.StopPreviewer(ctx)
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

func (p *dockerProject) packBuild(
	ctx context.Context,
	svc *ServiceConfig,
	dockerOptions DockerProjectOptions,
	imageName string) (*ServiceBuildResult, error) {
	previewer := p.console.ShowPreviewer(ctx,
		&input.ShowPreviewerOptions{
			Prefix:       "  ",
			MaxLineCount: 8,
			Title:        "Docker (pack) Output",
		})
	err := p.pack.Build(ctx, filepath.Join(svc.Path(), dockerOptions.Context), BuilderImage, imageName, previewer)
	p.console.StopPreviewer(ctx)
	if err != nil {
		return nil, err
	}

	imageId, err := p.docker.Inspect(ctx, imageName, "{{.Id}}")
	if err != nil {
		return nil, err
	}
	imageId = strings.TrimSpace(imageId)

	return &ServiceBuildResult{
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
			task.SetProgress(NewServiceProgress("Tagging Docker image"))
			if err := p.docker.Tag(ctx, serviceConfig.Path(), imageId, localTag); err != nil {
				task.SetError(err)
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
		options.Platform = docker.DefaultPlatform
	}

	if options.Context == "" {
		options.Context = "."
	}

	return options
}

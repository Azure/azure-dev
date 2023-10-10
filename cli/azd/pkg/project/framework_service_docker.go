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

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/pack"
	"go.opentelemetry.io/otel/trace"
)

type DockerProjectOptions struct {
	Path      string           `yaml:"path,omitempty"      json:"path,omitempty"`
	Context   string           `yaml:"context,omitempty"   json:"context,omitempty"`
	Platform  string           `yaml:"platform,omitempty"  json:"platform,omitempty"`
	Tag       ExpandableString `yaml:"tag,omitempty"       json:"tag,omitempty"`
	BuildArgs []string         `yaml:"buildArgs,omitempty" json:"buildArgs,omitempty"`
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
	env                 *environment.Environment
	docker              docker.Docker
	framework           FrameworkService
	containerHelper     *ContainerHelper
	console             input.Console
	alphaFeatureManager *alpha.FeatureManager
	commandRunner       exec.CommandRunner
}

// NewDockerProject creates a new instance of a Azd project that
// leverages docker for building
func NewDockerProject(
	env *environment.Environment,
	docker docker.Docker,
	containerHelper *ContainerHelper,
	console input.Console,
	alphaFeatureManager *alpha.FeatureManager,
	commandRunner exec.CommandRunner,
) CompositeFrameworkService {
	return &dockerProject{
		env:                 env,
		docker:              docker,
		containerHelper:     containerHelper,
		console:             console,
		alphaFeatureManager: alphaFeatureManager,
		commandRunner:       commandRunner,
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

			path := filepath.Join(serviceConfig.Path(), dockerOptions.Path)
			_, err := os.Stat(path)
			packBuildEnabled := p.alphaFeatureManager.IsEnabled(alpha.Buildpacks)
			if packBuildEnabled {
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					task.SetError(fmt.Errorf("reading dockerfile: %w", err))
					return
				}
			} else {
				if err != nil {
					task.SetError(fmt.Errorf("reading dockerfile: %w", err))
					return
				}
			}

			if packBuildEnabled && errors.Is(err, os.ErrNotExist) {
				// Build the container from source
				task.SetProgress(NewServiceProgress("Building Docker image from source"))
				res, err := p.packBuild(ctx, serviceConfig, dockerOptions, imageName)
				if err != nil {
					task.SetError(err)
					return
				}

				res.Restore = restoreOutput
				task.SetResult(res)
				return
			}

			// Build the container
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

// Default builder image to produce container images from source
const DefaultBuilderImage = "mcr.microsoft.com/oryx/builder:debian-bullseye-20230830.1"
const DefaultDotNetBuilderImage = "mcr.microsoft.com/oryx/builder:debian-buster-20230830.1"

func (p *dockerProject) packBuild(
	ctx context.Context,
	svc *ServiceConfig,
	dockerOptions DockerProjectOptions,
	imageName string) (*ServiceBuildResult, error) {
	pack, err := pack.NewPackCli(ctx, p.console, p.commandRunner)
	if err != nil {
		return nil, err
	}
	builder := DefaultBuilderImage
	if svc.Language == ServiceLanguageDotNet {
		builder = DefaultDotNetBuilderImage
	}

	environ := []string{}
	userDefinedImage := false
	if os.Getenv("AZD_BUILDER_IMAGE") != "" {
		builder = os.Getenv("AZD_BUILDER_IMAGE")
		userDefinedImage = true
	}

	previewer := p.console.ShowPreviewer(ctx,
		&input.ShowPreviewerOptions{
			Prefix:       "  ",
			MaxLineCount: 8,
			Title:        "Docker (pack) Output",
		})

	ctx, span := tracing.Start(
		ctx,
		events.PackBuildEvent,
		trace.WithAttributes(fields.ProjectServiceLanguageKey.String(string(svc.Language))))

	img, tag := docker.SplitDockerImage(builder)
	if userDefinedImage {
		span.SetAttributes(
			fields.StringHashed(fields.PackBuilderImage, img),
			fields.StringHashed(fields.PackBuilderTag, tag),
		)
	} else {
		span.SetAttributes(
			fields.PackBuilderImage.String(img),
			fields.PackBuilderTag.String(tag),
		)
	}

	err = pack.Build(
		ctx,
		svc.Path(),
		builder,
		imageName,
		environ,
		previewer)
	p.console.StopPreviewer(ctx)
	if err != nil {
		span.EndWithStatus(err)
		return nil, err
	}

	span.End()

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

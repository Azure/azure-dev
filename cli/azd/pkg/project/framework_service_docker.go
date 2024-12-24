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
	"path"
	"path/filepath"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/pack"
	"go.opentelemetry.io/otel/trace"
)

type DockerProjectOptions struct {
	Path        string                    `yaml:"path,omitempty"        json:"path,omitempty"`
	Context     string                    `yaml:"context,omitempty"     json:"context,omitempty"`
	Platform    string                    `yaml:"platform,omitempty"    json:"platform,omitempty"`
	Target      string                    `yaml:"target,omitempty"      json:"target,omitempty"`
	Registry    osutil.ExpandableString   `yaml:"registry,omitempty"    json:"registry,omitempty"`
	Image       osutil.ExpandableString   `yaml:"image,omitempty"       json:"image,omitempty"`
	Tag         osutil.ExpandableString   `yaml:"tag,omitempty"         json:"tag,omitempty"`
	RemoteBuild bool                      `yaml:"remoteBuild,omitempty" json:"remoteBuild,omitempty"`
	BuildArgs   []osutil.ExpandableString `yaml:"buildArgs,omitempty"   json:"buildArgs,omitempty"`
	// not supported from azure.yaml directly yet. Adding it for Aspire to use it, initially.
	// Aspire would pass the secret keys, which are env vars that azd will set just to run docker build.
	BuildSecrets []string `yaml:"-"                     json:"-"`
	BuildEnv     []string `yaml:"-"                     json:"-"`
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
	// The image hash that is generated from a docker build
	ImageHash string `json:"imageHash"`
	// The external source image specified when not building from source
	SourceImage string `json:"sourceImage"`
	// The target image with tag that is used for publishing and deployment when targeting a container registry
	TargetImage string `json:"targetImage"`
}

func (dpr *dockerPackageResult) ToString(currentIndentation string) string {
	builder := strings.Builder{}
	if dpr.ImageHash != "" {
		builder.WriteString(fmt.Sprintf("%s- Image Hash: %s\n", currentIndentation, output.WithLinkFormat(dpr.ImageHash)))
	}

	if dpr.SourceImage != "" {
		builder.WriteString(
			fmt.Sprintf("%s- Source Image: %s\n",
				currentIndentation,
				output.WithLinkFormat(dpr.SourceImage),
			),
		)
	}

	if dpr.TargetImage != "" {
		builder.WriteString(
			fmt.Sprintf("%s- Target Image: %s\n",
				currentIndentation,
				output.WithLinkFormat(dpr.TargetImage),
			),
		)
	}

	return builder.String()
}

func (dpr *dockerPackageResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(*dpr)
}

type dockerProject struct {
	env                 *environment.Environment
	docker              *docker.Cli
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
	docker *docker.Cli,
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
		framework:           NewNoOpProject(env),
	}
}

// NewDockerProjectAsFrameworkService is the same as NewDockerProject().(FrameworkService) and exists to support our
// use of DI and ServiceLocators, where we sometimes need to resolve this type as a FrameworkService instance instead
// of a CompositeFrameworkService as [NewDockerProject] does.
func NewDockerProjectAsFrameworkService(
	env *environment.Environment,
	docker *docker.Cli,
	containerHelper *ContainerHelper,
	console input.Console,
	alphaFeatureManager *alpha.FeatureManager,
	commandRunner exec.CommandRunner,
) FrameworkService {
	return NewDockerProject(env, docker, containerHelper, console, alphaFeatureManager, commandRunner)
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
func (p *dockerProject) RequiredExternalTools(ctx context.Context, sc *ServiceConfig) []tools.ExternalTool {
	return p.containerHelper.RequiredExternalTools(ctx, sc)
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
	progress *async.Progress[ServiceProgress],
) (*ServiceRestoreResult, error) {
	// When the program runs the restore actions for the underlying project (containerapp),
	// the dependencies are installed locally
	return p.framework.Restore(ctx, serviceConfig, progress)
}

// Builds the docker project based on the docker options specified within the Service configuration
func (p *dockerProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	restoreOutput *ServiceRestoreResult,
	progress *async.Progress[ServiceProgress],
) (*ServiceBuildResult, error) {
	if serviceConfig.Docker.RemoteBuild || useDotnetPublishForDockerBuild(serviceConfig) {
		return &ServiceBuildResult{Restore: restoreOutput}, nil
	}

	dockerOptions := getDockerOptionsWithDefaults(serviceConfig.Docker)

	resolveParameters := func(source []string) ([]string, error) {
		result := make([]string, len(source))
		for i, arg := range source {
			evaluatedString, err := apphost.EvalString(arg, func(match string) (string, error) {
				path := match
				value, has := p.env.Config.GetString(path)
				if !has {
					return "", fmt.Errorf("parameter %s not found", path)
				}
				return value, nil
			})
			if err != nil {
				return nil, err
			}
			result[i] = evaluatedString
		}
		return result, nil
	}

	dockerBuildArgs := []string{}
	for _, arg := range dockerOptions.BuildArgs {
		buildArgValue, err := arg.Envsubst(p.env.Getenv)
		if err != nil {
			return nil, fmt.Errorf("substituting environment variables in build args: %w", err)
		}

		dockerBuildArgs = append(dockerBuildArgs, buildArgValue)
	}

	// resolve parameters for build args and secrets
	resolvedBuildArgs, err := resolveParameters(dockerBuildArgs)
	if err != nil {
		return nil, err
	}

	resolvedBuildEnv, err := resolveParameters(dockerOptions.BuildEnv)
	if err != nil {
		return nil, err
	}

	dockerOptions.BuildEnv = resolvedBuildEnv

	// For services that do not specify a project path and have not specified a language then
	// there is nothing to build and we can return an empty build result
	// Ex) A container app project that uses an external image path
	if serviceConfig.RelativePath == "" &&
		(serviceConfig.Language == ServiceLanguageNone || serviceConfig.Language == ServiceLanguageDocker) {
		return &ServiceBuildResult{}, nil
	}

	buildArgs := []string{}
	for _, arg := range resolvedBuildArgs {
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

	dockerfilePath := dockerOptions.Path
	if !filepath.IsAbs(dockerfilePath) {
		dockerfilePath = filepath.Join(serviceConfig.Path(), dockerfilePath)
	}

	_, err = os.Stat(dockerfilePath)
	if errors.Is(err, os.ErrNotExist) && serviceConfig.Docker.Path == "" {
		// Build the container from source when:
		// 1. No Dockerfile path is specified, and
		// 2. <service directory>/Dockerfile doesn't exist
		progress.SetProgress(NewServiceProgress("Building Docker image from source"))
		res, err := p.packBuild(ctx, serviceConfig, dockerOptions, imageName)
		if err != nil {
			return nil, err
		}

		res.Restore = restoreOutput
		return res, nil
	}

	// Include full environment variables for the docker build including:
	// 1. Environment variables from the host
	// 2. Environment variables from the service configuration
	// 3. Environment variables from the docker configuration
	dockerEnv := []string{}
	dockerEnv = append(dockerEnv, os.Environ()...)
	dockerEnv = append(dockerEnv, p.env.Environ()...)
	dockerEnv = append(dockerEnv, dockerOptions.BuildEnv...)

	// Build the container
	progress.SetProgress(NewServiceProgress("Building Docker image"))
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
		dockerOptions.Target,
		dockerOptions.Context,
		imageName,
		resolvedBuildArgs,
		dockerOptions.BuildSecrets,
		dockerEnv,
		previewerWriter,
	)
	p.console.StopPreviewer(ctx, false)
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

func useDotnetPublishForDockerBuild(serviceConfig *ServiceConfig) bool {
	if serviceConfig.useDotNetPublishForDockerBuild != nil {
		return *serviceConfig.useDotNetPublishForDockerBuild
	}

	serviceConfig.useDotNetPublishForDockerBuild = to.Ptr(false)

	if serviceConfig.Language.IsDotNet() {
		projectPath := serviceConfig.Path()

		dockerOptions := getDockerOptionsWithDefaults(serviceConfig.Docker)

		dockerfilePath := dockerOptions.Path
		if !filepath.IsAbs(dockerfilePath) {
			s, err := os.Stat(projectPath)
			if err == nil && s.IsDir() {
				dockerfilePath = filepath.Join(projectPath, dockerfilePath)
			} else {
				dockerfilePath = filepath.Join(filepath.Dir(projectPath), dockerfilePath)
			}
		}

		if _, err := os.Stat(dockerfilePath); errors.Is(err, os.ErrNotExist) {
			serviceConfig.useDotNetPublishForDockerBuild = to.Ptr(true)
		}
	}

	return *serviceConfig.useDotNetPublishForDockerBuild
}

func (p *dockerProject) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	if serviceConfig.Docker.RemoteBuild || useDotnetPublishForDockerBuild(serviceConfig) {
		return &ServicePackageResult{Build: buildOutput}, nil
	}

	var imageId string

	if buildOutput != nil {
		imageId = buildOutput.BuildOutputPath
	}

	packageDetails := &dockerPackageResult{
		ImageHash: imageId,
	}

	// If we don't have an image ID from a docker build then an external source image is being used
	if imageId == "" {
		sourceImageValue, err := serviceConfig.Image.Envsubst(p.env.Getenv)
		if err != nil {
			return nil, fmt.Errorf("substituting environment variables in image: %w", err)
		}

		sourceImage, err := docker.ParseContainerImage(sourceImageValue)
		if err != nil {
			return nil, fmt.Errorf("parsing source container image: %w", err)
		}

		remoteImageUrl := sourceImage.Remote()

		progress.SetProgress(NewServiceProgress("Pulling container source image"))
		if err := p.docker.Pull(ctx, remoteImageUrl); err != nil {
			return nil, fmt.Errorf("pulling source container image: %w", err)
		}

		imageId = remoteImageUrl
		packageDetails.SourceImage = remoteImageUrl
	}

	// Generate a local tag from the 'docker' configuration section of the service
	imageWithTag, err := p.containerHelper.LocalImageTag(ctx, serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("generating local image tag: %w", err)
	}

	// Tag image.
	log.Printf("tagging image %s as %s", imageId, imageWithTag)
	progress.SetProgress(NewServiceProgress("Tagging container image"))
	if err := p.docker.Tag(ctx, serviceConfig.Path(), imageId, imageWithTag); err != nil {
		return nil, fmt.Errorf("tagging image: %w", err)
	}

	packageDetails.TargetImage = imageWithTag

	return &ServicePackageResult{
		Build:       buildOutput,
		PackagePath: packageDetails.SourceImage,
		Details:     packageDetails,
	}, nil
}

// Default builder image to produce container images from source, needn't java jdk storage, use the standard bp
const DefaultBuilderImage = "mcr.microsoft.com/oryx/builder:debian-bullseye-20240424.1"

func (p *dockerProject) packBuild(
	ctx context.Context,
	svc *ServiceConfig,
	dockerOptions DockerProjectOptions,
	imageName string) (*ServiceBuildResult, error) {
	packCli, err := pack.NewCli(ctx, p.console, p.commandRunner)
	if err != nil {
		return nil, err
	}
	builder := DefaultBuilderImage

	buildContext := svc.Path()
	environ := []string{}
	userDefinedImage := false
	if os.Getenv("AZD_BUILDER_IMAGE") != "" {
		builder = os.Getenv("AZD_BUILDER_IMAGE")
		userDefinedImage = true
	}

	if !userDefinedImage {
		// Always default to port 80 for consistency across languages
		environ = append(environ, "ORYX_RUNTIME_PORT=80")

		// For multi-module project, specify parent directory and submodule for pack build
		if svc.ParentPath != "" {
			buildContext = svc.ParentPath
			svcRelPath, err := filepath.Rel(buildContext, svc.Path())
			if err != nil {
				return nil, err
			}
			environ = append(environ, fmt.Sprintf("BP_MAVEN_BUILT_MODULE=%s", filepath.ToSlash(svcRelPath)))
		}

		if svc.Language == ServiceLanguageJava {
			environ = append(environ, "ORYX_RUNTIME_PORT=8080")
		}

		if svc.OutputPath != "" && (svc.Language == ServiceLanguageTypeScript || svc.Language == ServiceLanguageJavaScript) {
			inDockerOutputPath := path.Join("/workspace", svc.OutputPath)
			// A dist folder has been set.
			// We assume that the service is a front-end service, configuring a nginx web server to serve the static content
			// produced.
			environ = append(environ,
				"ORYX_RUNTIME_IMAGE=nginx:1.25.2-bookworm",
				fmt.Sprintf(
					//nolint:lll
					"ORYX_RUNTIME_SCRIPT=[ -d \"%s\" ] || { echo \"error: directory '%s' does not exist. ensure the 'dist' path in azure.yaml is specified correctly.\"; exit 1; } && "+
						"rm -rf /usr/share/nginx/html && ln -sT %s /usr/share/nginx/html && "+
						"nginx -g 'daemon off;'",
					inDockerOutputPath,
					svc.OutputPath,
					inDockerOutputPath,
				))
		}

		if svc.Language == ServiceLanguagePython {
			pyEnviron, err := getEnvironForPython(ctx, svc)
			if err != nil {
				return nil, err
			}
			if len(pyEnviron) > 0 {
				environ = append(environ, pyEnviron...)
			}
		}
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

	err = packCli.Build(
		ctx,
		buildContext,
		builder,
		imageName,
		environ,
		previewer)
	p.console.StopPreviewer(ctx, false)
	if err != nil {
		span.EndWithStatus(err)

		var statusCodeErr *pack.StatusCodeError
		if errors.As(err, &statusCodeErr) && statusCodeErr.Code == pack.StatusCodeUndetectedNoError {
			return nil, &internal.ErrorWithSuggestion{
				Err: err,
				Suggestion: "No Dockerfile was found, and image could not be automatically built from source. " +
					fmt.Sprintf(
						"\nSuggested action: Author a Dockerfile and save it as %s",
						filepath.Join(svc.Path(), dockerOptions.Path)),
			}
		}

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

func getEnvironForPython(ctx context.Context, svc *ServiceConfig) ([]string, error) {
	prj, err := appdetect.DetectDirectory(ctx, svc.Path())
	if err != nil {
		return nil, err
	}

	if prj == nil { // Undetected project, resume build from the Oryx builder
		return nil, nil
	}

	// Support for FastAPI apps since the Oryx builder does not support it yet
	for _, dep := range prj.Dependencies {
		if dep == appdetect.PyFastApi {
			launch, err := appdetect.PyFastApiLaunch(prj.Path)
			if err != nil {
				return nil, err
			}

			// If launch isn't detected, fallback to default Oryx runtime logic, which may recover for scenarios
			// such as a simple main entrypoint launch.
			if launch == "" {
				return nil, nil
			}

			return []string{
				"POST_BUILD_COMMAND=pip install uvicorn",
				//nolint:lll
				"ORYX_RUNTIME_SCRIPT=oryx create-script -appPath ./oryx-output -bindPort 80 -userStartupCommand " +
					"'uvicorn " + launch + " --port $PORT --host $HOST' && ./run.sh"}, nil
		}
	}

	return nil, nil
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

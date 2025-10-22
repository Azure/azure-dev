// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/containerregistry"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/pack"
	"github.com/benbjohnson/clock"
	"github.com/sethvargo/go-retry"
	"go.opentelemetry.io/otel/trace"
)

// PublishOptions holds options for container operations such as publish and deploy.
type PublishOptions struct {
	// Image specifies the target image in the form '[registry/]repository[:tag]'
	Image string
}

type ContainerHelper struct {
	env                      *environment.Environment
	envManager               environment.Manager
	remoteBuildManager       *containerregistry.RemoteBuildManager
	containerRegistryService azapi.ContainerRegistryService
	commandRunner            exec.CommandRunner
	docker                   *docker.Cli
	dotNetCli                *dotnet.Cli
	clock                    clock.Clock
	console                  input.Console
	cloud                    *cloud.Cloud
}

func NewContainerHelper(
	env *environment.Environment,
	envManager environment.Manager,
	clock clock.Clock,
	containerRegistryService azapi.ContainerRegistryService,
	remoteBuildManager *containerregistry.RemoteBuildManager,
	commandRunner exec.CommandRunner,
	docker *docker.Cli,
	dotNetCli *dotnet.Cli,
	console input.Console,
	cloud *cloud.Cloud,
) *ContainerHelper {
	return &ContainerHelper{
		env:                      env,
		envManager:               envManager,
		remoteBuildManager:       remoteBuildManager,
		containerRegistryService: containerRegistryService,
		commandRunner:            commandRunner,
		docker:                   docker,
		dotNetCli:                dotNetCli,
		clock:                    clock,
		console:                  console,
		cloud:                    cloud,
	}
}

// DefaultImageName returns a default image name generated from the service name and environment name.
func (ch *ContainerHelper) DefaultImageName(serviceConfig *ServiceConfig) string {
	return fmt.Sprintf("%s/%s-%s",
		strings.ToLower(serviceConfig.Project.Name),
		strings.ToLower(serviceConfig.Name),
		strings.ToLower(ch.env.Name()))
}

// DefaultImageTag returns a default image tag generated from the current time.
func (ch *ContainerHelper) DefaultImageTag() string {
	return fmt.Sprintf("azd-deploy-%d", ch.clock.Now().Unix())
}

// RegistryName returns the name of the destination container registry to use for the current environment from the following:
// 1. AZURE_CONTAINER_REGISTRY_ENDPOINT environment variable
// 2. docker.registry from the service configuration
func (ch *ContainerHelper) RegistryName(ctx context.Context, serviceConfig *ServiceConfig) (string, error) {
	registryName, found := ch.env.LookupEnv(environment.ContainerRegistryEndpointEnvVarName)
	if !found {
		log.Printf(
			"Container registry not found in '%s' environment variable\n",
			environment.ContainerRegistryEndpointEnvVarName,
		)
	}

	if registryName == "" {
		yamlRegistryName, err := serviceConfig.Docker.Registry.Envsubst(ch.env.Getenv)
		if err != nil {
			log.Println("Failed expanding 'docker.registry'")
		}

		registryName = yamlRegistryName
	}

	// If the service provides its own code artifacts then the expectation is that an images needs to be built and
	// pushed to a container registry.
	// If the service does not provide its own code artifacts then the expectation is a registry is optional and
	// an image can be referenced independently.
	if serviceConfig.RelativePath != "" && registryName == "" {
		return "", fmt.Errorf(
			//nolint:lll
			"could not determine container registry endpoint, ensure 'registry' has been set in the docker options or '%s' environment variable has been set",
			environment.ContainerRegistryEndpointEnvVarName,
		)
	}

	return registryName, nil
}

// GeneratedImage returns the configured image from the service configuration
// or a default image name generated from the service name and environment name.
func (ch *ContainerHelper) GeneratedImage(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) (*docker.ContainerImage, error) {
	// Parse the image from azure.yaml configuration when available
	configuredImage, err := serviceConfig.Docker.Image.Envsubst(ch.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing 'image' from docker configuration, %w", err)
	}

	// Set default image name if not configured
	if configuredImage == "" {
		configuredImage = ch.DefaultImageName(serviceConfig)
	}

	parsedImage, err := docker.ParseContainerImage(configuredImage)
	if err != nil {
		return nil, fmt.Errorf("failed parsing configured image, %w", err)
	}

	if parsedImage.Tag == "" {
		configuredTag, err := serviceConfig.Docker.Tag.Envsubst(ch.env.Getenv)
		if err != nil {
			return nil, fmt.Errorf("failed parsing 'tag' from docker configuration, %w", err)
		}

		// Set default tag if not configured
		if configuredTag == "" {
			configuredTag = ch.DefaultImageTag()
		}

		parsedImage.Tag = configuredTag
	}

	// Set default registry if not configured
	if parsedImage.Registry == "" {
		// This can fail if called before provisioning the registry
		configuredRegistry, err := ch.RegistryName(ctx, serviceConfig)
		if err == nil {
			parsedImage.Registry = configuredRegistry
		}
	}

	return parsedImage, nil
}

// RemoteImageTag returns the remote image tag for the service configuration.
func (ch *ContainerHelper) RemoteImageTag(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	localImageTag string,
	imageOverride *imageOverride,
) (string, error) {
	registryName, err := ch.RegistryName(ctx, serviceConfig)
	if err != nil {
		return "", err
	}

	containerImage, err := docker.ParseContainerImage(localImageTag)
	if err != nil {
		return "", err
	}

	if registryName != "" {
		containerImage.Registry = registryName
	}

	// Apply overrides
	if imageOverride != nil {
		if imageOverride.Registry != "" {
			containerImage.Registry = imageOverride.Registry
		}
		if imageOverride.Repository != "" {
			containerImage.Repository = imageOverride.Repository
		}
		if imageOverride.Tag != "" {
			containerImage.Tag = imageOverride.Tag
		}
	}

	return containerImage.Remote(), nil
}

// LocalImageTag returns the local image tag for the service configuration.
func (ch *ContainerHelper) LocalImageTag(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) (string, error) {
	configuredImage, err := ch.GeneratedImage(ctx, serviceConfig)
	if err != nil {
		return "", err
	}

	return configuredImage.Local(), nil
}

func (ch *ContainerHelper) RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) []tools.ExternalTool {
	if serviceConfig.Docker.RemoteBuild {
		return []tools.ExternalTool{}
	}

	if useDotnetPublishForDockerBuild(serviceConfig) {
		return []tools.ExternalTool{ch.dotNetCli}
	}

	return []tools.ExternalTool{ch.docker}
}

// Login logs into the container registry specified by AZURE_CONTAINER_REGISTRY_ENDPOINT in the environment. On success,
// it returns the name of the container registry that was logged into.
func (ch *ContainerHelper) Login(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) (string, error) {
	registryName, err := ch.RegistryName(ctx, serviceConfig)
	if err != nil {
		return "", err
	}

	// Only perform automatic login for ACR
	// Other registries require manual login via external 'docker login' command
	hostParts := strings.Split(registryName, ".")
	if len(hostParts) == 1 || strings.HasSuffix(registryName, ch.cloud.ContainerRegistryEndpointSuffix) {
		return registryName, ch.containerRegistryService.Login(ctx, ch.env.GetSubscriptionId(), registryName)
	}

	return registryName, nil
}

var defaultCredentialsRetryDelay = 20 * time.Second

func (ch *ContainerHelper) Credentials(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) (*azapi.DockerCredentials, error) {
	loginServer, err := ch.RegistryName(ctx, serviceConfig)
	if err != nil {
		return nil, err
	}

	var credential *azapi.DockerCredentials
	credentialsError := retry.Do(
		ctx,
		// will retry just once after 1 minute based on:
		// https://learn.microsoft.com/en-us/azure/dns/dns-faq#how-long-does-it-take-for-dns-changes-to-take-effect-
		retry.WithMaxRetries(3, retry.NewConstant(defaultCredentialsRetryDelay)),
		func(ctx context.Context) error {
			cred, err := ch.containerRegistryService.Credentials(ctx, targetResource.SubscriptionId(), loginServer)
			if err != nil {
				var httpErr *azcore.ResponseError
				if errors.As(err, &httpErr) {
					if httpErr.StatusCode == 404 {
						// Retry if the registry is not found while logging in
						return retry.RetryableError(err)
					}
				}
				return err
			}
			credential = cred
			return nil
		})

	return credential, credentialsError
}

func (ch *ContainerHelper) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServiceBuildResult, error) {
	if serviceConfig.Docker.RemoteBuild || useDotnetPublishForDockerBuild(serviceConfig) {
		return &ServiceBuildResult{}, nil
	}

	dockerOptions := getDockerOptionsWithDefaults(serviceConfig.Docker)

	resolveParameters := func(source []string) ([]string, error) {
		result := make([]string, len(source))
		for i, arg := range source {
			evaluatedString, err := apphost.EvalString(arg, func(match string) (string, error) {
				path := match
				value, has := ch.env.Config.GetString(path)
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
		buildArgValue, err := arg.Envsubst(ch.env.Getenv)
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
		res, err := ch.packBuild(ctx, serviceConfig, dockerOptions, imageName)
		if err != nil {
			return nil, err
		}

		return res, nil
	}

	// Include full environment variables for the docker build including:
	// 1. Environment variables from the host
	// 2. Environment variables from the service configuration
	// 3. Environment variables from the docker configuration
	dockerEnv := []string{}
	dockerEnv = append(dockerEnv, os.Environ()...)
	dockerEnv = append(dockerEnv, ch.env.Environ()...)
	dockerEnv = append(dockerEnv, dockerOptions.BuildEnv...)

	// Build the container
	progress.SetProgress(NewServiceProgress("Building Docker image"))
	previewerWriter := ch.console.ShowPreviewer(ctx,
		&input.ShowPreviewerOptions{
			Prefix:       "  ",
			MaxLineCount: 8,
			Title:        "Docker Output",
		})

	dockerFilePath := dockerOptions.Path
	if dockerOptions.InMemDockerfile != nil {
		// when using an in-memory dockerfile, we write it to a temp file and use that path for the build
		tempDir, err := os.MkdirTemp("", "dockerfile-for-"+serviceConfig.Name)
		if err != nil {
			return nil, fmt.Errorf("creating temp dir for dockerfile for service %s: %w", serviceConfig.Name, err)
		}
		// use the name of the original dockerfile path
		dockerfilePath = filepath.Join(tempDir, filepath.Base(dockerFilePath))
		err = os.WriteFile(dockerfilePath, dockerOptions.InMemDockerfile, osutil.PermissionFileOwnerOnly)
		if err != nil {
			return nil, fmt.Errorf("writing dockerfile for service %s: %w", serviceConfig.Name, err)
		}
		dockerFilePath = dockerfilePath

		log.Println("using in-memory dockerfile for build", dockerfilePath)

		// ensure we clean up the temp dockerfile after the build
		defer func() {
			if err := os.RemoveAll(tempDir); err != nil {
				log.Printf("removing temp dockerfile dir %s: %v", tempDir, err)
			}
		}()
	}

	imageId, err := ch.docker.Build(
		ctx,
		serviceConfig.Path(),
		dockerFilePath,
		dockerOptions.Platform,
		dockerOptions.Target,
		dockerOptions.Context,
		imageName,
		resolvedBuildArgs,
		dockerOptions.BuildSecrets,
		dockerEnv,
		previewerWriter,
	)
	ch.console.StopPreviewer(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("building container: %s at %s: %w", serviceConfig.Name, dockerOptions.Context, err)
	}

	log.Printf("built image %s for %s", imageId, serviceConfig.Name)

	// Create container image artifact for build output
	return &ServiceBuildResult{
		Artifacts: ArtifactCollection{{
			Kind:         ArtifactKindContainer,
			Location:     imageId,
			LocationKind: LocationKindLocal,
			Metadata: map[string]string{
				"imageId":   imageId,
				"imageName": imageName,
				"framework": "docker",
			},
		}},
	}, nil
}

func (ch *ContainerHelper) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	if serviceConfig.Docker.RemoteBuild || useDotnetPublishForDockerBuild(serviceConfig) {
		return &ServicePackageResult{}, nil
	}

	var imageId string
	var sourceImage string
	var imageHash string

	// Find the container image artifact from build results
	if artifact, found := serviceContext.Build.FindFirst(WithKind(ArtifactKindContainer)); found && artifact.Location != "" {
		imageId = artifact.Location
		imageHash = imageId // For built images, the location is the image hash
	}

	// If we don't have an image ID from a docker build then an external source image is being used
	if imageId == "" {
		sourceImageValue, err := serviceConfig.Image.Envsubst(ch.env.Getenv)
		if err != nil {
			return nil, fmt.Errorf("substituting environment variables in image: %w", err)
		}

		sourceImageContainer, err := docker.ParseContainerImage(sourceImageValue)
		if err != nil {
			return nil, fmt.Errorf("parsing source container image: %w", err)
		}

		remoteImageUrl := sourceImageContainer.Remote()

		progress.SetProgress(NewServiceProgress("Pulling container source image"))
		if err := ch.docker.Pull(ctx, remoteImageUrl); err != nil {
			return nil, fmt.Errorf("pulling source container image: %w", err)
		}

		imageId = remoteImageUrl // For tagging purposes
		sourceImage = remoteImageUrl
		imageHash = "" // External images don't have a known image hash
	}

	// Generate a local tag from the 'docker' configuration section of the service
	imageWithTag, err := ch.LocalImageTag(ctx, serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("generating local image tag: %w", err)
	}

	// Tag image.
	log.Printf("tagging image %s as %s", imageId, imageWithTag)
	progress.SetProgress(NewServiceProgress("Tagging container image"))
	if err := ch.docker.Tag(ctx, serviceConfig.Path(), imageId, imageWithTag); err != nil {
		return nil, fmt.Errorf("tagging image: %w", err)
	}

	targetImage := imageWithTag

	// Create container image artifact
	packageArtifact := &Artifact{
		Kind:         ArtifactKindContainer,
		Location:     imageWithTag,
		LocationKind: LocationKindLocal, // Local during package phase
		Metadata: map[string]string{
			"imageHash":   imageHash,
			"sourceImage": sourceImage,
			"targetImage": targetImage,
		},
	}

	return &ServicePackageResult{
		Artifacts: ArtifactCollection{packageArtifact},
	}, nil
}

// Publish pushes an image to a remote server and returns the fully qualified remote image name.
func (ch *ContainerHelper) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
	options *PublishOptions,
) (*ServicePublishResult, error) {
	var remoteImage string
	var err error

	// Parse PublishOptions into ImageOverride
	imageOverride, err := parseImageOverride(options)
	if err != nil {
		return nil, err
	}

	if serviceConfig.Docker.RemoteBuild {
		remoteImage, err = ch.runRemoteBuild(ctx, serviceConfig, targetResource, progress, imageOverride)
	} else if useDotnetPublishForDockerBuild(serviceConfig) {
		remoteImage, err = ch.runDotnetPublish(ctx, serviceConfig, targetResource, progress)
	} else {
		remoteImage, err = ch.publishLocalImage(ctx, serviceConfig, serviceContext, progress, imageOverride)
	}
	if err != nil {
		return nil, err
	}

	// Create publish artifact with remote image reference
	publishArtifact := &Artifact{
		Kind:         ArtifactKindContainer,
		Location:     remoteImage,
		LocationKind: LocationKindRemote, // Remote after publish
		Metadata: map[string]string{
			"remoteImage": remoteImage,
		},
	}

	return &ServicePublishResult{
		Artifacts: ArtifactCollection{publishArtifact},
	}, nil
}

// publishLocalImage builds the image locally and pushes it to the remote registry, it returns the full remote image name.
func (ch *ContainerHelper) publishLocalImage(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
	imageOverride *imageOverride,
) (string, error) {
	// Get ACR Login Server
	registryName, err := ch.RegistryName(ctx, serviceConfig)
	if err != nil {
		return "", err
	}

	var sourceImage string
	var targetImage string

	// Find the container image artifact from package results
	if artifact, found := serviceContext.Package.FindFirst(WithKind(ArtifactKindContainer)); found {
		targetImage = artifact.Location
		if sourceImageValue := artifact.Metadata["sourceImage"]; sourceImageValue != "" {
			sourceImage = sourceImageValue
		}
		// Fall back to metadata targetImage if Location is empty
		if targetImage == "" {
			if targetImageValue := artifact.Metadata["targetImage"]; targetImageValue != "" {
				targetImage = targetImageValue
			}
		}
	}

	// Default to the local image tag
	remoteImage := targetImage

	// If we don't have a registry specified and the service does not reference a project path
	// then we are referencing a public/pre-existing image and don't have anything to tag or push
	if registryName == "" && serviceConfig.RelativePath == "" && sourceImage != "" {
		remoteImage = sourceImage
	} else {
		if targetImage == "" {
			return "", errors.New("failed retrieving package result details")
		}

		// If a registry has not been defined then there is no need to tag or push any images
		if registryName != "" {
			// When the project does not contain source and we are using an external image we first need to pull the
			// image before we're able to push it to a remote registry
			// In most cases this pull will have already been part of the package step
			if sourceImage != "" && serviceConfig.RelativePath == "" {
				progress.SetProgress(NewServiceProgress("Pulling container image"))
				err = ch.docker.Pull(ctx, sourceImage)
				if err != nil {
					return "", fmt.Errorf("pulling image: %w", err)
				}
			}

			// Tag image
			// Get remote remoteImageWithTag from the container helper then call docker cli remoteImageWithTag command
			remoteImageWithTag, err := ch.RemoteImageTag(ctx, serviceConfig, targetImage, imageOverride)
			if err != nil {
				return "", fmt.Errorf("getting remote image tag: %w", err)
			}

			remoteImage = remoteImageWithTag

			progress.SetProgress(NewServiceProgress("Tagging container image"))
			if err := ch.docker.Tag(ctx, serviceConfig.Path(), targetImage, remoteImage); err != nil {
				return "", err
			}

			log.Printf("logging into container registry '%s'\n", registryName)
			progress.SetProgress(NewServiceProgress("Logging into container registry"))

			_, err = ch.Login(ctx, serviceConfig)
			if err != nil {
				return "", err
			}

			// Push image.
			log.Printf("pushing %s to registry", remoteImage)
			progress.SetProgress(NewServiceProgress("Pushing container image"))
			if err := ch.docker.Push(ctx, serviceConfig.Path(), remoteImage); err != nil {
				errSuggestion := &internal.ErrorWithSuggestion{
					Err: err,
					//nolint:lll
					Suggestion: "When pushing to an external registry, ensure you have successfully authenticated by calling 'docker login' and run 'azd deploy' again",
				}

				return "", errSuggestion
			}
		}
	}

	return remoteImage, nil
}

type imageOverride docker.ContainerImage

// parseImageOverride parses the PublishOptions.Image string into an ImageOverride.
// Supports combinations of:
// - Registry, repository, tag all present (registry.com/repo/name:tag)
// - Repository and tag present (repo/name:tag)
// - Registry and repository present (registry.com/repo/name)
// - Only repository present (repo/name)
func parseImageOverride(options *PublishOptions) (*imageOverride, error) {
	if options == nil || options.Image == "" {
		return nil, nil
	}

	// Parse the container image using the existing parser
	parsedImage, err := docker.ParseContainerImage(options.Image)
	if err != nil {
		return nil, fmt.Errorf("invalid image format '%s': %w", options.Image, err)
	}

	// Convert to ImageOverride
	return &imageOverride{
		Registry:   parsedImage.Registry,
		Repository: parsedImage.Repository,
		Tag:        parsedImage.Tag,
	}, nil
}

// runRemoteBuild builds the image using a remote azure container registry and tags it.
// It returns the full remote image name.
func (ch *ContainerHelper) runRemoteBuild(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	target *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
	imageOverride *imageOverride,
) (string, error) {
	dockerOptions := getDockerOptionsWithDefaults(serviceConfig.Docker)

	if !filepath.IsAbs(dockerOptions.Path) {
		dockerOptions.Path = filepath.Join(serviceConfig.Path(), dockerOptions.Path)
	}

	if !filepath.IsAbs(dockerOptions.Context) {
		dockerOptions.Context = filepath.Join(serviceConfig.Path(), dockerOptions.Context)
	}

	if dockerOptions.Platform != "linux/amd64" {
		return "", fmt.Errorf("remote build only supports the linux/amd64 platform")
	}

	progress.SetProgress(NewServiceProgress("Packing remote build context"))

	contextPath, dockerPath, err := containerregistry.PackRemoteBuildSource(ctx, dockerOptions.Context, dockerOptions.Path)
	if contextPath != "" {
		defer os.Remove(contextPath)
	}
	if err != nil {
		return "", err
	}

	progress.SetProgress(NewServiceProgress("Uploading remote build context"))

	registryName, err := ch.RegistryName(ctx, serviceConfig)
	if err != nil {
		return "", err
	}

	acrRegistryDomain := "." + ch.cloud.ContainerRegistryEndpointSuffix

	if !strings.HasSuffix(registryName, acrRegistryDomain) {
		return "", fmt.Errorf("remote build is only supported when the target registry is an Azure Container Registry")
	}

	registryResourceName := strings.TrimSuffix(registryName, acrRegistryDomain)

	source, err := ch.remoteBuildManager.UploadBuildSource(
		ctx, target.SubscriptionId(), target.ResourceGroupName(), registryResourceName, contextPath)
	if err != nil {
		return "", err
	}

	localImageTag, err := ch.LocalImageTag(ctx, serviceConfig)
	if err != nil {
		return "", err
	}

	imageName, err := ch.RemoteImageTag(ctx, serviceConfig, localImageTag, imageOverride)
	if err != nil {
		return "", err
	}

	progress.SetProgress(NewServiceProgress("Running remote build"))

	buildRequest := &armcontainerregistry.DockerBuildRequest{
		SourceLocation: source.RelativePath,
		DockerFilePath: to.Ptr(dockerPath),
		IsPushEnabled:  to.Ptr(true),
		ImageNames:     []*string{to.Ptr(imageName)},
		Platform: &armcontainerregistry.PlatformProperties{
			OS:           to.Ptr(armcontainerregistry.OSLinux),
			Architecture: to.Ptr(armcontainerregistry.ArchitectureAmd64),
		},
	}

	previewerWriter := ch.console.ShowPreviewer(ctx,
		&input.ShowPreviewerOptions{
			Prefix:       "  ",
			MaxLineCount: 8,
			Title:        "Docker Output",
		})
	err = ch.remoteBuildManager.RunDockerBuildRequestWithLogs(
		ctx, target.SubscriptionId(), target.ResourceGroupName(), registryResourceName, buildRequest, previewerWriter)
	ch.console.StopPreviewer(ctx, false)
	if err != nil {
		return "", err
	}

	return imageName, nil
}

// runDotnetPublish builds and publishes the container image using `dotnet publish`. It returns the full remote image name.
func (ch *ContainerHelper) runDotnetPublish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	target *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) (string, error) {
	progress.SetProgress(NewServiceProgress("Logging into registry"))

	dockerCreds, err := ch.Credentials(ctx, serviceConfig, target)
	if err != nil {
		return "", fmt.Errorf("logging in to registry: %w", err)
	}

	progress.SetProgress(NewServiceProgress("Publishing container image"))

	imageName := fmt.Sprintf("%s:%s",
		ch.DefaultImageName(serviceConfig),
		ch.DefaultImageTag())

	_, err = ch.dotNetCli.PublishContainer(
		ctx,
		serviceConfig.Path(),
		"Release",
		imageName,
		dockerCreds.LoginServer,
		dockerCreds.Username,
		dockerCreds.Password)
	if err != nil {
		return "", fmt.Errorf("publishing container: %w", err)
	}

	return fmt.Sprintf("%s/%s", dockerCreds.LoginServer, imageName), nil
}

// Default builder image to produce container images from source, needn't java jdk storage, use the standard bp
const DefaultBuilderImage = "mcr.microsoft.com/oryx/builder:debian-bullseye-20240424.1"

func (ch *ContainerHelper) packBuild(
	ctx context.Context,
	svc *ServiceConfig,
	dockerOptions DockerProjectOptions,
	imageName string) (*ServiceBuildResult, error) {
	packCli, err := pack.NewCli(ctx, ch.console, ch.commandRunner)
	if err != nil {
		return nil, err
	}
	builder := DefaultBuilderImage
	environ := []string{}
	userDefinedImage := false

	if os.Getenv("AZD_BUILDER_IMAGE") != "" {
		builder = os.Getenv("AZD_BUILDER_IMAGE")
		userDefinedImage = true
	}

	svcPath := svc.Path()
	buildContext := svcPath

	if svc.Docker.Context != "" {
		buildContext = svc.Docker.Context

		if !filepath.IsAbs(buildContext) {
			buildContext = filepath.Join(svcPath, buildContext)
		}
	}

	if !userDefinedImage {
		// Always default to port 80 for consistency across languages
		environ = append(environ, "ORYX_RUNTIME_PORT=80")

		if svc.Language == ServiceLanguageJava {
			environ = append(environ, "ORYX_RUNTIME_PORT=8080")

			if buildContext != svcPath {
				svcRelPath, err := filepath.Rel(buildContext, svcPath)
				if err != nil {
					return nil, fmt.Errorf("calculating relative context path: %w", err)
				}

				environ = append(environ, fmt.Sprintf("BP_MAVEN_BUILT_MODULE=%s", filepath.ToSlash(svcRelPath)))
			}
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

	previewer := ch.console.ShowPreviewer(ctx,
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
	ch.console.StopPreviewer(ctx, false)
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

		// Provide better error message for containerd-related issues
		if strings.Contains(err.Error(), "failed to write image") && strings.Contains(err.Error(), "No such image") {
			isContainerdEnabled, containerdErr := ch.docker.IsContainerdEnabled(ctx)
			if containerdErr != nil {
				log.Printf("warning: failed to detect containerd status: %v", containerdErr)
			} else if isContainerdEnabled {
				return nil, &internal.ErrorWithSuggestion{
					Err: err,
					Suggestion: "Suggestion: disable containerd image store in Docker settings: " +
						output.WithLinkFormat("https://docs.docker.com/desktop/features/containerd"),
				}
			}
		}

		return nil, err
	}

	span.End()

	imageId, err := ch.docker.Inspect(ctx, imageName, "{{.Id}}")
	if err != nil {
		return nil, err
	}
	imageId = strings.TrimSpace(imageId)

	// Create container image artifact for build output
	return &ServiceBuildResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindContainer,
				Location:     imageId,
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"imageId":   imageId,
					"imageName": imageName,
					"framework": "docker",
				},
			}},
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

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/containerregistry"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/benbjohnson/clock"
	"github.com/sethvargo/go-retry"
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

func (ch *ContainerHelper) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	if serviceConfig.Docker.RemoteBuild || useDotnetPublishForDockerBuild(serviceConfig) {
		return &ServicePackageResult{Artifacts: serviceContext.Build}, nil
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
		remoteImage, err = ch.runLocalBuild(ctx, serviceConfig, serviceContext, progress, imageOverride)
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

// runLocalBuild builds the image locally and pushes it to the remote registry, it returns the full remote image name.
func (ch *ContainerHelper) runLocalBuild(
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

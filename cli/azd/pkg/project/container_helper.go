package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/sethvargo/go-retry"
)

type ContainerHelper struct {
	env                      *environment.Environment
	envManager               environment.Manager
	imageHelper              *ImageHelper
	containerRegistryService azcli.ContainerRegistryService
	docker                   docker.Docker
	cloud                    *cloud.Cloud
}

func NewContainerHelper(
	env *environment.Environment,
	envManager environment.Manager,
	imageHelper *ImageHelper,
	containerRegistryService azcli.ContainerRegistryService,
	docker docker.Docker,
	cloud *cloud.Cloud,
) *ContainerHelper {
	return &ContainerHelper{
		env:                      env,
		envManager:               envManager,
		imageHelper:              imageHelper,
		containerRegistryService: containerRegistryService,
		docker:                   docker,
		cloud:                    cloud,
	}
}

func (ch *ContainerHelper) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{ch.docker}
}

// Login logs into the container registry specified by AZURE_CONTAINER_REGISTRY_ENDPOINT in the environment. On success,
// it returns the name of the container registry that was logged into.
func (ch *ContainerHelper) Login(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) (string, error) {
	registryName, err := ch.imageHelper.RegistryName(ctx, serviceConfig)
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
) (*azcli.DockerCredentials, error) {
	loginServer, err := ch.imageHelper.RegistryName(ctx, serviceConfig)
	if err != nil {
		return nil, err
	}

	var credential *azcli.DockerCredentials
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

// Deploy pushes and image to a remote server, and optionally writes the fully qualified remote image name to the
// environment on success.
func (ch *ContainerHelper) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
	writeImageToEnv bool,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	// Get ACR Login Server
	registryName, err := ch.imageHelper.RegistryName(ctx, serviceConfig)
	if err != nil {
		return nil, err
	}

	var sourceImage string
	targetImage := packageOutput.PackagePath

	packageDetails, ok := packageOutput.Details.(*dockerPackageResult)
	if ok && packageDetails != nil {
		sourceImage = packageDetails.SourceImage
		targetImage = packageDetails.TargetImage
	}

	// Default to the local image tag
	remoteImage := targetImage

	// If we don't have a registry specified and the service does not reference a project path
	// then we are referencing a public/pre-existing image and don't have anything to tag or push
	if registryName == "" && serviceConfig.RelativePath == "" && sourceImage != "" {
		remoteImage = sourceImage
	} else {
		if targetImage == "" {
			return nil, errors.New("failed retrieving package result details")
		}

		// If a registry has not been defined then there is no need to tag or push any images
		if registryName != "" {
			// When the project does not contain source and we are using an external image we first need to pull the image
			// before we're able to push it to a remote registry
			// In most cases this pull will have already been part of the package step
			if packageDetails != nil && serviceConfig.RelativePath == "" {
				progress.SetProgress(NewServiceProgress("Pulling container image"))
				err = ch.docker.Pull(ctx, sourceImage)
				if err != nil {
					return nil, fmt.Errorf("pulling image: %w", err)
				}
			}

			// Tag image
			// Get remote remoteImageWithTag from the container helper then call docker cli remoteImageWithTag command
			remoteImageWithTag, err := ch.imageHelper.RemoteImageTag(ctx, serviceConfig, targetImage)
			if err != nil {
				return nil, fmt.Errorf("getting remote image tag: %w", err)
			}

			remoteImage = remoteImageWithTag

			progress.SetProgress(NewServiceProgress("Tagging container image"))
			if err := ch.docker.Tag(ctx, serviceConfig.Path(), targetImage, remoteImage); err != nil {
				return nil, err
			}

			log.Printf("logging into container registry '%s'\n", registryName)
			progress.SetProgress(NewServiceProgress("Logging into container registry"))

			_, err = ch.Login(ctx, serviceConfig)
			if err != nil {
				return nil, err
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

				return nil, errSuggestion
			}
		}
	}

	if writeImageToEnv {
		// Save the name of the image we pushed into the environment with a well known key.
		log.Printf("writing image name to environment")
		ch.env.SetServiceProperty(serviceConfig.Name, "IMAGE_NAME", remoteImage)

		if err := ch.envManager.Save(ctx, ch.env); err != nil {
			return nil, fmt.Errorf("saving image name to environment: %w", err)
		}
	}

	return &ServiceDeployResult{
		Package: packageOutput,
		Details: &dockerDeployResult{
			RemoteImageTag: remoteImage,
		},
	}, nil
}

type dockerDeployResult struct {
	RemoteImageTag string
}

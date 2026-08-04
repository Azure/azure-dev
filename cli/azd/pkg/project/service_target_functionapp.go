// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/denormal/go-gitignore"
)

// resolveFunctionAppRemoteBuild returns the appropriate remote build setting for function apps.
func resolveFunctionAppRemoteBuild(serviceConfig *ServiceConfig) (remoteBuild bool, err error) {
	switch serviceConfig.Language {
	case ServiceLanguageJavaScript, ServiceLanguageTypeScript:
		ignoreFile := serviceConfig.Host.IgnoreFile()
		ignoreFilePath := filepath.Join(serviceConfig.Path(), ignoreFile)
		ignoreFileContents, err := os.ReadFile(ignoreFilePath)
		if errors.Is(err, fs.ErrNotExist) {
			if serviceConfig.RemoteBuild != nil {
				// no ignore file, nothing to validate -- return true
				return *serviceConfig.RemoteBuild, nil
			}

			// no ignore file, default to true
			return true, nil
		}

		if err != nil {
			return false, fmt.Errorf("reading ignore file: %w", err)
		}

		// Strip UTF-8 BOM if present. Some Windows editors (e.g. Notepad) prepend a BOM which
		// causes the gitignore parser to treat the first pattern as having invisible prefix bytes,
		// breaking pattern matching.
		ignoreFileContents = stripUTF8BOM(ignoreFileContents)

		// Parse from in-memory contents so we don't hold an open file handle (important on Windows temp dirs).
		ignore := gitignore.New(bytes.NewReader(ignoreFileContents), serviceConfig.Path(), nil)

		nodeModulesExcluded := false
		if match := ignore.Relative("node_modules", true); match != nil && match.Ignore() {
			nodeModulesExcluded = true
		}

		if serviceConfig.RemoteBuild == nil { // remoteBuild option unset
			// enable remote build only if 'node_modules' is excluded
			return nodeModulesExcluded, nil
		}

		if *serviceConfig.RemoteBuild && !nodeModulesExcluded {
			return false, &internal.ErrorWithSuggestion{
				Err:        fmt.Errorf("'remoteBuild: true' requires '%s' to exclude node_modules", ignoreFile),
				Suggestion: fmt.Sprintf("Update '%s' to exclude node_modules, or set 'remoteBuild: false'.", ignoreFile),
			}
		}

		if !*serviceConfig.RemoteBuild && nodeModulesExcluded {
			return false, &internal.ErrorWithSuggestion{
				Err:        fmt.Errorf("'remoteBuild: false' cannot be used when '%s' excludes node_modules", ignoreFile),
				Suggestion: fmt.Sprintf("Set 'remoteBuild: true', or remove node_modules from '%s'.", ignoreFile),
			}
		}

		return *serviceConfig.RemoteBuild, nil
	case ServiceLanguageGo:
		// Go compiles to a static binary — remote build is not supported
		if serviceConfig.RemoteBuild != nil && *serviceConfig.RemoteBuild {
			return false, &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"remote build is not supported for Go function apps",
				),
				Suggestion: "Go compiles to a static binary locally. Set 'remoteBuild: false' or remove the setting.",
			}
		}
		return false, nil
	default:
		if serviceConfig.RemoteBuild != nil {
			return *serviceConfig.RemoteBuild, nil
		}

		return serviceConfig.Language == ServiceLanguagePython, nil
	}
}

// functionAppTarget specifies an Azure Function to deploy to.
// Implements `project.ServiceTarget`
type functionAppTarget struct {
	env     *environment.Environment
	cli     *azapi.AzureClient
	console input.Console
	// containerTarget handles the phases that are identical to App Service container deployments
	// (tool discovery, packaging, and publishing the image to ACR). Deployment is handled locally
	// because Function Apps do not support azd's App Service deployment slot workflow.
	containerTarget ServiceTarget
}

// NewFunctionAppTarget creates a new instance of the Function App target
func NewFunctionAppTarget(
	env *environment.Environment,
	envManager environment.Manager,
	containerHelper *ContainerHelper,
	azCli *azapi.AzureClient,
	console input.Console,
) ServiceTarget {
	return &functionAppTarget{
		env:             env,
		cli:             azCli,
		console:         console,
		containerTarget: NewAppServiceTarget(env, envManager, containerHelper, azCli, console),
	}
}

// Gets the required external tools for the Function app
func (f *functionAppTarget) RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) []tools.ExternalTool {
	if containerConfigured(serviceConfig) {
		return f.containerTarget.RequiredExternalTools(ctx, serviceConfig)
	}
	return []tools.ExternalTool{}
}

// Initializes the function app target
func (f *functionAppTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	if err := validateFunctionAppContainerConfig(serviceConfig); err != nil {
		return err
	}
	if containerConfigured(serviceConfig) {
		return f.containerTarget.Initialize(ctx, serviceConfig)
	}
	return nil
}

// validateFunctionAppContainerConfig rejects service configurations that mix code and container
// deployment settings, before any Azure resources are contacted.
func validateFunctionAppContainerConfig(serviceConfig *ServiceConfig) error {
	if !serviceConfig.Image.Empty() &&
		serviceConfig.Language != ServiceLanguageNone &&
		serviceConfig.Language != ServiceLanguageDocker {
		return &internal.ErrorWithSuggestion{
			Err: fmt.Errorf(
				"pre-built image deployments cannot use source language '%s'",
				serviceConfig.Language,
			),
			Suggestion: "Remove 'language' or set 'language: docker' when using 'image'.",
		}
	}

	if containerConfigured(serviceConfig) && serviceConfig.RemoteBuild != nil {
		return &internal.ErrorWithSuggestion{
			Err:        fmt.Errorf("top-level 'remoteBuild' is only supported for code-based function deployments"),
			Suggestion: "Remove the top-level setting and use 'docker.remoteBuild' for container deployments.",
		}
	}

	return nil
}

// Prepares a zip archive from the specified build output
func (f *functionAppTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	if isContainerDeploy(serviceConfig, serviceContext) {
		return f.containerTarget.Package(ctx, serviceConfig, serviceContext, progress)
	}

	// Extract build artifact from service context
	var buildPath string
	if artifact, found := serviceContext.Package.FindFirst(WithKind(ArtifactKindDirectory)); found {
		buildPath = artifact.Location
	}
	if buildPath == "" {
		return nil, fmt.Errorf("no build result found in service context")
	}

	var err error
	zipFilePath := buildPath
	if filepath.Ext(buildPath) != ".zip" {
		progress.SetProgress(NewServiceProgress("Compressing deployment artifacts"))
		zipFilePath, err = createDeployableZip(
			serviceConfig,
			buildPath,
		)
		if err != nil {
			return nil, err
		}
	}

	return &ServicePackageResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindArchive,
				Location:     zipFilePath,
				LocationKind: LocationKindLocal,
			},
		},
	}, nil
}

func (f *functionAppTarget) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
	publishOptions *PublishOptions,
) (*ServicePublishResult, error) {
	if isContainerDeploy(serviceConfig, serviceContext) {
		return f.containerTarget.Publish(
			ctx,
			serviceConfig,
			serviceContext,
			targetResource,
			progress,
			publishOptions,
		)
	}

	return &ServicePublishResult{}, nil
}

// Deploys the packaged service to the Azure Function App resource. Container services update the
// site's image reference; code services are uploaded through zip deploy.
func (f *functionAppTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	if err := f.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	// Fetch site properties up front so an incompatible site is reported before any upload is
	// attempted. Zip deploying to a container site leaves the deployment API polling indefinitely.
	props, err := f.cli.GetFunctionAppProperties(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
	)
	if err != nil {
		return nil, fmt.Errorf("fetching function app properties: %w", err)
	}

	if isContainerDeploy(serviceConfig, serviceContext) {
		return f.containerDeploy(ctx, serviceConfig, serviceContext, targetResource, props, progress)
	}

	return f.zipDeploy(ctx, serviceConfig, serviceContext, targetResource, props, progress)
}

// containerDeploy points the Function App at the container image published to the registry.
// Unlike App Service, Function Apps always deploy to the main site: azd does not support deployment
// slots for this host, so no slot resolution or prompting takes place.
func (f *functionAppTarget) containerDeploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	props *azapi.AzCliFunctionAppProperties,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	artifact, found := serviceContext.Publish.FindFirst(WithKind(ArtifactKindContainer))
	if !found || artifact.Location == "" {
		return nil, fmt.Errorf("no container image found in publish artifacts for service: %s", serviceConfig.Name)
	}

	if !props.ContainerConfiguration.IsContainer {
		return nil, &internal.ErrorWithSuggestion{
			Err: fmt.Errorf(
				"function app '%s' is configured for zip deployment, "+
					"but service '%s' is configured for container deployment",
				targetResource.ResourceName(),
				serviceConfig.Name,
			),
			Suggestion: "Update your infrastructure to provision a Linux function app whose 'linuxFxVersion' " +
				"is set to a 'DOCKER|<image>' value, or configure the service for zip deployment by removing " +
				"'language: docker', 'docker.path', and 'image'.",
		}
	}

	progress.SetProgress(NewServiceProgress("Updating container image"))
	if err := f.cli.UpdateAppServiceContainerImage(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		artifact.Location,
	); err != nil {
		return nil, fmt.Errorf("deploying container to function app %s: %w", serviceConfig.Name, err)
	}

	progress.SetProgress(NewServiceProgress("Fetching endpoints for function app"))
	endpoints, err := f.Endpoints(ctx, serviceConfig, targetResource)
	if err != nil {
		return nil, err
	}

	artifacts, err := newDeployArtifacts(endpoints, targetResource)
	if err != nil {
		return nil, err
	}

	return &ServiceDeployResult{
		Artifacts: artifacts,
	}, nil
}

// zipDeploy uploads the packaged zip archive using the Function App deployment API.
func (f *functionAppTarget) zipDeploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	props *azapi.AzCliFunctionAppProperties,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	if props.ContainerConfiguration.IsContainer {
		return nil, &internal.ErrorWithSuggestion{
			Err: fmt.Errorf(
				"function app '%s' is configured for container deployment, "+
					"but service '%s' is configured for zip deployment",
				targetResource.ResourceName(),
				serviceConfig.Name,
			),
			Suggestion: "Configure the service for container deployment using 'language: docker', " +
				"'docker.path', or 'image'.",
		}
	}

	// Extract zip package from service context
	var zipFilePath string
	if artifact, found := serviceContext.Package.FindFirst(WithKind(ArtifactKindArchive)); found {
		zipFilePath = artifact.Location
	}
	if zipFilePath == "" {
		return nil, fmt.Errorf("no zip package found in service context")
	}

	zipFile, err := os.Open(zipFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed reading deployment zip file: %w", err)
	}

	defer zipFile.Close()

	plan, err := f.cli.GetFunctionAppPlan(ctx, props)
	if err != nil {
		return nil, fmt.Errorf("determining function app plan type: %w", err)
	}

	isFlexConsumption := strings.EqualFold(*plan.SKU.Tier, "flexconsumption")

	if serviceConfig.RemoteBuild != nil && !isFlexConsumption {
		return nil, &internal.ErrorWithSuggestion{
			Err: fmt.Errorf("'remoteBuild' is only supported for Flex Consumption plan function apps"),
			Suggestion: "For other plan types, set these environment variables on the function app:\n" +
				"  ENABLE_ORYX_BUILD=true\n" +
				"  SCM_DO_BUILD_DURING_DEPLOYMENT=true",
		}
	}

	progress.SetProgress(NewServiceProgress("Uploading deployment package"))

	// Deploy to appropriate plan type
	if isFlexConsumption {
		remoteBuild, buildErr := resolveFunctionAppRemoteBuild(serviceConfig)
		if buildErr != nil {
			return nil, buildErr
		}

		_, err = f.cli.DeployFunctionAppUsingZipFileFlexConsumption(
			ctx,
			targetResource.SubscriptionId(),
			props,
			targetResource.ResourceName(),
			zipFile,
			remoteBuild,
		)
	} else {
		_, err = f.cli.DeployFunctionAppUsingZipFileRegular(
			ctx,
			targetResource.SubscriptionId(),
			props,
			targetResource.ResourceName(),
			zipFile,
		)
	}
	if err != nil {
		return nil, err
	}

	progress.SetProgress(NewServiceProgress("Fetching endpoints for function app"))
	endpoints, err := f.Endpoints(ctx, serviceConfig, targetResource)
	if err != nil {
		return nil, err
	}

	artifacts, err := newDeployArtifacts(endpoints, targetResource)
	if err != nil {
		return nil, err
	}

	return &ServiceDeployResult{
		Artifacts: artifacts,
	}, nil
}

// Gets the exposed endpoints for the Function App
func (f *functionAppTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	// TODO(azure/azure-dev#670) Implement this. For now we just return an empty set of endpoints and
	// a nil error.  In `deploy` we just loop over the endpoint array and print any endpoints, so returning
	// an empty array and nil error will mean "no endpoints".
	if props, err := f.cli.GetFunctionAppProperties(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName()); err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	} else {
		endpoints := make([]string, len(props.HostNames))
		for idx, hostName := range props.HostNames {
			endpoints[idx] = fmt.Sprintf("https://%s/", hostName)
		}

		return endpoints, nil
	}
}

func (f *functionAppTarget) validateTargetResource(
	targetResource *environment.TargetResource,
) error {
	if !strings.EqualFold(targetResource.ResourceType(), string(azapi.AzureResourceTypeWebSite)) {
		return resourceTypeMismatchError(
			targetResource.ResourceName(),
			targetResource.ResourceType(),
			azapi.AzureResourceTypeWebSite,
		)
	}

	return nil
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/denormal/go-gitignore"
)

const functionAppRemoteBuildDocURL = "https://aka.ms/azd-functionapp-remote-build"

// resolveFunctionAppRemoteBuild returns the appropriate remote build setting for function apps.
func resolveFunctionAppRemoteBuild(serviceConfig *ServiceConfig) (remoteBuild bool, err error) {
	switch serviceConfig.Language {
	case ServiceLanguageJavaScript, ServiceLanguageTypeScript:
		ignore, err := gitignore.NewFromFile(filepath.Join(serviceConfig.Path(), serviceConfig.Host.IgnoreFile()))
		if errors.Is(err, fs.ErrNotExist) {
			// no ignore file, default to true
			return true, nil
		}

		if err != nil {
			return false, fmt.Errorf("reading ignore file: %w", err)
		}

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
				Err: fmt.Errorf("'remoteBuild: true' requires '.funcignore' to exclude node_modules"),
				Suggestion: fmt.Sprintf(
					"Update '.funcignore' to exclude node_modules, or set 'remoteBuild: false'. Learn more: %s",
					output.WithLinkFormat(functionAppRemoteBuildDocURL),
				),
			}
		}

		if !*serviceConfig.RemoteBuild && nodeModulesExcluded {
			return false, &internal.ErrorWithSuggestion{
				Err: fmt.Errorf("'remoteBuild: false' cannot be used when '.funcignore' excludes node_modules"),
				Suggestion: fmt.Sprintf(
					"Set 'remoteBuild: true', or remove node_modules from '.funcignore'. Learn more: %s",
					output.WithLinkFormat(functionAppRemoteBuildDocURL),
				),
			}
		}

		return *serviceConfig.RemoteBuild, nil
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
}

// NewFunctionAppTarget creates a new instance of the Function App target
func NewFunctionAppTarget(
	env *environment.Environment,
	azCli *azapi.AzureClient,
	console input.Console,
) ServiceTarget {
	return &functionAppTarget{
		env:     env,
		cli:     azCli,
		console: console,
	}
}

// Gets the required external tools for the Function app
func (f *functionAppTarget) RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) []tools.ExternalTool {
	return []tools.ExternalTool{}
}

// Initializes the function app target
func (f *functionAppTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

// Prepares a zip archive from the specified build output
func (f *functionAppTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
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
	return &ServicePublishResult{}, nil
}

// Deploys the prepared zip archive using Zip deploy to the Azure App Service resource
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

	props, err := f.cli.GetFunctionAppProperties(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
	)
	if err != nil {
		return nil, fmt.Errorf("fetching function app properties: %w", err)
	}

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

	artifacts := ArtifactCollection{}

	// Add endpoints as artifacts
	for _, endpoint := range endpoints {
		if err := artifacts.Add(&Artifact{
			Kind:         ArtifactKindEndpoint,
			Location:     endpoint,
			LocationKind: LocationKindRemote,
		}); err != nil {
			return nil, fmt.Errorf("failed to add endpoint artifact: %w", err)
		}
	}

	// Add resource artifact
	var resourceArtifact *Artifact
	if err := mapper.Convert(targetResource, &resourceArtifact); err == nil {
		if err := artifacts.Add(resourceArtifact); err != nil {
			return nil, fmt.Errorf("failed to add resource artifact: %w", err)
		}
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

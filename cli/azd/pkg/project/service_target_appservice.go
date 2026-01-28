// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type appServiceTarget struct {
	env     *environment.Environment
	cli     *azapi.AzureClient
	console input.Console
}

// NewAppServiceTarget creates a new instance of the AppServiceTarget
func NewAppServiceTarget(
	env *environment.Environment,
	azCli *azapi.AzureClient,
	console input.Console,
) ServiceTarget {
	return &appServiceTarget{
		env:     env,
		cli:     azCli,
		console: console,
	}
}

// Gets the required external tools
func (st *appServiceTarget) RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) []tools.ExternalTool {
	return []tools.ExternalTool{}
}

// Initializes the AppService target
func (st *appServiceTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

// Prepares a zip archive from the specified build output
func (st *appServiceTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	progress.SetProgress(NewServiceProgress("Compressing deployment artifacts"))

	// Get package path from the service context
	var packagePath string
	if artifact, found := serviceContext.Package.FindFirst(WithKind(ArtifactKindDirectory)); found &&
		artifact.Location != "" {
		packagePath = artifact.Location
	}

	if packagePath == "" {
		return nil, fmt.Errorf("no package artifacts found in service context")
	}

	zipFilePath, err := createDeployableZip(
		serviceConfig,
		packagePath,
	)
	if err != nil {
		return nil, err
	}

	// Create zip artifact
	zipArtifact := &Artifact{
		Kind:         ArtifactKindArchive,
		Location:     zipFilePath,
		LocationKind: LocationKindLocal,
		Metadata: map[string]string{
			"packagePath": packagePath,
		},
	}

	return &ServicePackageResult{
		Artifacts: ArtifactCollection{zipArtifact},
	}, nil
}

func (st *appServiceTarget) Publish(
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
func (st *appServiceTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	if err := st.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	// Get zip file path from package artifacts
	var zipFilePath string
	if artifact, found := serviceContext.Package.FindFirst(WithKind(ArtifactKindArchive)); found && artifact.Location != "" {
		zipFilePath = artifact.Location
	}

	if zipFilePath == "" {
		return nil, fmt.Errorf("no zip artifacts found in service context")
	}

	// Determine deployment targets based on deployment history and slots
	deployTargets, err := st.determineDeploymentTargets(ctx, targetResource, progress)
	if err != nil {
		return nil, fmt.Errorf("determining deployment targets: %w", err)
	}

	// Deploy to each target
	for _, target := range deployTargets {
		zipFile, err := os.Open(zipFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed reading deployment zip file: %w", err)
		}
		defer zipFile.Close()

		var deployErr error
		if target.SlotName == "" {
			progress.SetProgress(NewServiceProgress("Uploading deployment package to main app"))
			_, deployErr = st.cli.DeployAppServiceZip(
				ctx,
				targetResource.SubscriptionId(),
				targetResource.ResourceGroupName(),
				targetResource.ResourceName(),
				zipFile,
				func(logProgress string) { progress.SetProgress(NewServiceProgress(logProgress)) },
			)
		} else {
			progressMsg := fmt.Sprintf("Uploading deployment package to slot '%s'", target.SlotName)
			progress.SetProgress(NewServiceProgress(progressMsg))
			_, deployErr = st.cli.DeployAppServiceSlotZip(
				ctx,
				targetResource.SubscriptionId(),
				targetResource.ResourceGroupName(),
				targetResource.ResourceName(),
				target.SlotName,
				zipFile,
				func(logProgress string) { progress.SetProgress(NewServiceProgress(logProgress)) },
			)
		}

		if deployErr != nil {
			targetName := "main app"
			if target.SlotName != "" {
				targetName = fmt.Sprintf("slot '%s'", target.SlotName)
			}
			return nil, fmt.Errorf("deploying service %s to %s: %w", serviceConfig.Name, targetName, deployErr)
		}
	}

	progress.SetProgress(NewServiceProgress("Fetching endpoints for app service"))
	endpoints, err := st.Endpoints(ctx, serviceConfig, targetResource)
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

// deploymentTarget represents a target for deployment (main app or a slot)
type deploymentTarget struct {
	SlotName string // Empty string means main app
}

// determineDeploymentTargets determines which targets (main app and/or slots) to deploy to
// based on deployment history and available slots.
//
// Deployment Strategy:
//   - First deployment (no history):
//     Deploy to main app AND all slots to ensure consistency across all environments.
//     This prevents configuration drift and ensures all slots start with the same baseline.
//   - Subsequent deployments with no slots:
//     Deploy to main app only (standard production deployment).
//   - Subsequent deployments with exactly one slot:
//     Deploy to that slot only (typical staging workflow before swap to production).
//   - Subsequent deployments with multiple slots:
//     Prompt user to select a target (including main app), allowing explicit control
//     over which environment receives the deployment.
func (st *appServiceTarget) determineDeploymentTargets(
	ctx context.Context,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) ([]deploymentTarget, error) {
	progress.SetProgress(NewServiceProgress("Checking deployment history"))

	// Check if there are previous deployments
	hasDeployments, err := st.cli.HasAppServiceDeployments(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
	)
	if err != nil {
		return nil, fmt.Errorf("checking deployment history: %w", err)
	}

	progress.SetProgress(NewServiceProgress("Checking deployment slots"))

	// Get available slots
	slots, err := st.cli.GetAppServiceSlots(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
	)
	if err != nil {
		return nil, fmt.Errorf("getting deployment slots: %w", err)
	}

	// If no previous deployments, always deploy to main app and all slots
	if !hasDeployments {
		targets := []deploymentTarget{{SlotName: ""}} // Main app
		for _, slot := range slots {
			targets = append(targets, deploymentTarget{SlotName: slot.Name})
		}
		return targets, nil
	}

	// Has previous deployments
	if len(slots) == 0 {
		// No slots, deploy to main app only
		return []deploymentTarget{{SlotName: ""}}, nil
	}

	if len(slots) == 1 {
		// Exactly one slot, deploy to that slot only
		return []deploymentTarget{{SlotName: slots[0].Name}}, nil
	}

	// Multiple slots, prompt user to select (including main app as an option)
	slotOptions := make([]string, len(slots)+1)
	slotOptions[0] = "production (main app)"
	for i, slot := range slots {
		slotOptions[i+1] = slot.Name
	}

	selectedIndex, err := st.console.Select(ctx, input.ConsoleOptions{
		Message: "Select a deployment slot",
		Options: slotOptions,
	})
	if err != nil {
		return nil, fmt.Errorf("selecting deployment slot: %w", err)
	}

	// If user selected "production (main app)", return empty SlotName
	if selectedIndex == 0 {
		return []deploymentTarget{{SlotName: ""}}, nil
	}

	// Otherwise, return the selected slot
	return []deploymentTarget{{SlotName: slots[selectedIndex-1].Name}}, nil
}

// Gets the exposed endpoints for the App Service
func (st *appServiceTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	appServiceProperties, err := st.cli.GetAppServiceProperties(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
	)
	if err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	}

	endpoints := make([]string, len(appServiceProperties.HostNames))
	for idx, hostName := range appServiceProperties.HostNames {
		endpoints[idx] = fmt.Sprintf("https://%s/", hostName)
	}

	return endpoints, nil
}

func (st *appServiceTarget) validateTargetResource(
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

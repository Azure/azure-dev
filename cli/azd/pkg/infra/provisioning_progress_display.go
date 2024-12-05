// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
)

// ProvisioningProgressDisplay displays interactive progress for an ongoing Azure provisioning operation.
type ProvisioningProgressDisplay struct {
	// demo mode, controls whether links to Azure Portal are displayed
	demoMode bool
	// Whether the deployment has started
	deploymentDisplayed map[string]bool
	// Total number of deployments
	totalDeployments int
	// Keeps track of created resources
	displayedResources map[string]bool
	resourceManager    ResourceManager
	console            input.Console
}

func NewProvisioningProgressDisplay(
	rm ResourceManager,
	console input.Console,
	demoMode bool,
	totalDeployments int,
) *ProvisioningProgressDisplay {
	return &ProvisioningProgressDisplay{
		displayedResources:  map[string]bool{},
		deploymentDisplayed: map[string]bool{},
		resourceManager:     rm,
		console:             console,
		demoMode:            demoMode,
		totalDeployments:    totalDeployments,
	}
}

// ReportProgress reports the current deployment progress, setting the currently executing operation title and logging
// progress.
func (d *ProvisioningProgressDisplay) ReportProgress(
	ctx context.Context, deployment Deployment, queryStart *time.Time) error {
	name := deployment.Name()
	if d.demoMode && !d.deploymentDisplayed[name] {
		lines := []string{
			"You can view detailed progress in the Azure Portal:",
			"\n",
		}

		if d.totalDeployments > 1 {
			lines = []string{
				fmt.Sprintf("Deployment %d of %d:", len(d.deploymentDisplayed)+1, d.totalDeployments),
				"\n",
			}
		}

		d.console.EnsureBlankLine(ctx)
		d.console.MessageUxItem(
			ctx,
			&ux.MultilineMessage{
				Lines: lines,
			},
		)
		d.deploymentDisplayed[name] = true
	} else if !d.deploymentDisplayed[name] {
		deploymentUrl, err := deployment.DeploymentUrl(ctx)
		if err != nil {
			// Wait until deployment is live to display progress
			return nil
		}

		deploymentLink := fmt.Sprintf(output.WithLinkFormat("%s\n"), deploymentUrl)
		d.console.EnsureBlankLine(ctx)
		lines := []string{
			"You can view detailed progress in the Azure Portal:",
			deploymentLink,
		}

		if d.totalDeployments > 1 {
			lines = []string{
				fmt.Sprintf(
					"Deployment %d of %d. View detailed progress in the Azure Portal:",
					len(d.deploymentDisplayed)+1, d.totalDeployments),
				deploymentLink,
			}
		}
		d.console.MessageUxItem(
			ctx,
			&ux.MultilineMessage{
				Lines: lines,
			},
		)

		d.deploymentDisplayed[name] = true
	}

	operations, err := d.resourceManager.GetDeploymentResourceOperations(ctx, deployment, queryStart)
	if err != nil {
		// Status display is best-effort activity.
		return err
	}

	newlyDeployedResources := []*armresources.DeploymentOperation{}
	newlyFailedResources := []*armresources.DeploymentOperation{}
	runningDeployments := []*armresources.DeploymentOperation{}

	for i := range operations {
		if operations[i].Properties.TargetResource != nil {
			resourceId := *operations[i].Properties.TargetResource.ResourceName

			if !d.displayedResources[resourceId] {
				switch *operations[i].Properties.ProvisioningState {
				case string(armresources.ProvisioningStateSucceeded):
					newlyDeployedResources = append(newlyDeployedResources, operations[i])
				case string(armresources.ProvisioningStateRunning):
					runningDeployments = append(runningDeployments, operations[i])
				case string(armresources.ProvisioningStateFailed):
					newlyFailedResources = append(newlyFailedResources, operations[i])
				}
			}
		}
	}

	sort.Slice(newlyDeployedResources, func(i int, j int) bool {
		return time.Time.Before(
			*newlyDeployedResources[i].Properties.Timestamp,
			*newlyDeployedResources[j].Properties.Timestamp,
		)
	})

	displayedResources := append(newlyDeployedResources, newlyFailedResources...)
	d.logNewlyCreatedResources(ctx, displayedResources, deployment, runningDeployments)
	return nil
}

func (display *ProvisioningProgressDisplay) logNewlyCreatedResources(
	ctx context.Context,
	resources []*armresources.DeploymentOperation,
	deployment Deployment,
	inProgressResources []*armresources.DeploymentOperation,
) {
	for _, resource := range resources {
		resourceTypeName := *resource.Properties.TargetResource.ResourceType
		resourceTypeDisplayName, err := display.resourceManager.GetResourceTypeDisplayName(
			ctx,
			deployment.SubscriptionId(),
			*resource.Properties.TargetResource.ID,
			azapi.AzureResourceType(resourceTypeName),
		)

		if err != nil {
			// Dynamic resource type translation failed -- fallback to static translation
			resourceTypeDisplayName = azapi.GetResourceTypeDisplayName(azapi.AzureResourceType(resourceTypeName))
		}

		// Don't log resource types for Azure resources that we do not have a translation of the resource type for.
		// This will be improved on in a future iteration.
		if resourceTypeDisplayName != "" {
			duration, err := convert.ParseDuration(*resource.Properties.Duration)
			if err != nil {
				duration = 0
			}

			display.console.MessageUxItem(
				ctx,
				&ux.DisplayedResource{
					Type:     resourceTypeDisplayName,
					Name:     *resource.Properties.TargetResource.ResourceName,
					State:    ux.DisplayedResourceState(*resource.Properties.ProvisioningState),
					Duration: duration.Truncate(time.Millisecond),
				},
			)
			resourceTypeName = resourceTypeDisplayName
		}

		log.Printf(
			"%s - %s %s: %s",
			resource.Properties.Timestamp.Local().Format("2006-01-02 15:04:05"),
			*resource.Properties.ProvisioningState,
			resourceTypeName,
			*resource.Properties.TargetResource.ResourceName)

		display.displayedResources[*resource.Properties.TargetResource.ResourceName] = true
	}
	// update progress
	inProgress := []string{}
	for _, inProgResource := range inProgressResources {
		resourceTypeName := *inProgResource.Properties.TargetResource.ResourceType
		resourceTypeDisplayName, err := display.resourceManager.GetResourceTypeDisplayName(
			ctx,
			deployment.SubscriptionId(),
			*inProgResource.Properties.TargetResource.ID,
			azapi.AzureResourceType(resourceTypeName),
		)

		if err != nil {
			// Dynamic resource type translation failed -- fallback to static translation
			resourceTypeDisplayName = azapi.GetResourceTypeDisplayName(azapi.AzureResourceType(resourceTypeName))
		}

		// Don't log resource types for Azure resources that we do not have a translation of the resource type for.
		// This will be improved on in a future iteration.
		if resourceTypeDisplayName != "" {
			inProgress = append(inProgress, resourceTypeDisplayName)
		}
	}

	if !display.console.IsSpinnerInteractive() {
		// If non-interactive, we simply do not want to display spinner messages that ends up
		// being individual lines of messages on the console
		return
	}

	if len(inProgress) > 0 {
		display.console.ShowSpinner(ctx,
			fmt.Sprintf("Creating/Updating resources (%s)", strings.Join(inProgress, ", ")), input.Step)
	} else {
		display.console.ShowSpinner(ctx, "Creating/Updating resources", input.Step)
	}
}

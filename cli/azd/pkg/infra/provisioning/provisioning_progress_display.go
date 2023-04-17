// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
)

const defaultProgressTitle string = "Provisioning Azure resources"
const succeededProvisioningState string = "Succeeded"
const runningProvisioningState string = "Running"
const failedProvisioningState string = "Failed"

// ProvisioningProgressDisplay displays interactive progress for an ongoing Azure provisioning operation.
type ProvisioningProgressDisplay struct {
	// Whether the deployment has started
	deploymentStarted bool
	// Keeps track of created resources
	displayedResources map[string]bool
	resourceManager    infra.ResourceManager
	console            input.Console
	scope              infra.Scope
}

func NewProvisioningProgressDisplay(
	rm infra.ResourceManager,
	console input.Console,
	scope infra.Scope,
) ProvisioningProgressDisplay {
	return ProvisioningProgressDisplay{
		displayedResources: map[string]bool{},
		scope:              scope,
		resourceManager:    rm,
		console:            console,
	}
}

// ReportProgress reports the current deployment progress, setting the currently executing operation title and logging
// progress.
func (display *ProvisioningProgressDisplay) ReportProgress(
	ctx context.Context, queryStart *time.Time) (*DeployProgress, error) {
	progress := DeployProgress{
		Timestamp: time.Now(),
		Message:   defaultProgressTitle,
	}

	if !display.deploymentStarted {
		_, err := display.scope.GetDeployment(ctx)
		if err != nil {
			// Return default progress
			log.Printf("error while reporting progress: %s", err.Error())
			return &progress, nil
		}

		display.deploymentStarted = true
		deploymentUrl := fmt.Sprintf(
			output.WithLinkFormat("https://portal.azure.com/#blade/HubsExtension/DeploymentDetailsBlade/overview/id/%s\n"),
			url.PathEscape(display.scope.DeploymentUrl()),
		)

		display.console.MessageUxItem(
			ctx,
			&ux.MultilineMessage{
				Lines: []string{
					"You can view detailed progress in the Azure Portal:",
					deploymentUrl,
				},
			},
		)
	}

	operations, err := display.resourceManager.GetDeploymentResourceOperations(ctx, display.scope, queryStart)
	if err != nil {
		// Status display is best-effort activity.
		return &progress, err
	}

	newlyDeployedResources := []*armresources.DeploymentOperation{}
	newlyFailedResources := []*armresources.DeploymentOperation{}
	runningDeployments := []*armresources.DeploymentOperation{}

	for i := range operations {
		if operations[i].Properties.TargetResource != nil {
			resourceId := *operations[i].Properties.TargetResource.ResourceName

			if !display.displayedResources[resourceId] &&
				infra.IsTopLevelResourceType(
					infra.AzureResourceType(*operations[i].Properties.TargetResource.ResourceType)) {

				switch *operations[i].Properties.ProvisioningState {
				case succeededProvisioningState:
					newlyDeployedResources = append(newlyDeployedResources, operations[i])
				case runningProvisioningState:
					runningDeployments = append(runningDeployments, operations[i])
				case failedProvisioningState:
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
	display.logNewlyCreatedResources(ctx, displayedResources, runningDeployments)
	return &progress, nil
}

func (display *ProvisioningProgressDisplay) logNewlyCreatedResources(
	ctx context.Context,
	resources []*armresources.DeploymentOperation,
	inProgressResources []*armresources.DeploymentOperation,
) {
	for _, resource := range resources {
		resourceTypeName := *resource.Properties.TargetResource.ResourceType
		resourceTypeDisplayName, err := display.resourceManager.GetResourceTypeDisplayName(
			ctx,
			display.scope.SubscriptionId(),
			*resource.Properties.TargetResource.ID,
			infra.AzureResourceType(resourceTypeName),
		)

		if err != nil {
			// Dynamic resource type translation failed -- fallback to static translation
			resourceTypeDisplayName = infra.GetResourceTypeDisplayName(infra.AzureResourceType(resourceTypeName))
		}

		// Don't log resource types for Azure resources that we do not have a translation of the resource type for.
		// This will be improved on in a future iteration.
		if resourceTypeDisplayName != "" {
			display.console.MessageUxItem(
				ctx,
				&ux.DisplayedResource{
					Type:  resourceTypeDisplayName,
					Name:  *resource.Properties.TargetResource.ResourceName,
					State: ux.DisplayedResourceState(*resource.Properties.ProvisioningState),
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
			display.scope.SubscriptionId(),
			*inProgResource.Properties.TargetResource.ID,
			infra.AzureResourceType(resourceTypeName),
		)

		if err != nil {
			// Dynamic resource type translation failed -- fallback to static translation
			resourceTypeDisplayName = infra.GetResourceTypeDisplayName(infra.AzureResourceType(resourceTypeName))
		}

		// Don't log resource types for Azure resources that we do not have a translation of the resource type for.
		// This will be improved on in a future iteration.
		if resourceTypeDisplayName != "" {
			inProgress = append(inProgress, resourceTypeDisplayName)
		}
	}
	if len(inProgress) > 0 {
		display.console.ShowSpinner(ctx,
			fmt.Sprintf("Creating/Updating resources (%s)", strings.Join(inProgress, ", ")), input.Step)
	} else {
		display.console.ShowSpinner(ctx, "Creating/Updating resources", input.Step)
	}
}

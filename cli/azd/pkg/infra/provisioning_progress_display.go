// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"context"
	"fmt"
	"log"
	"os"
	"slices"
	"strconv"
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
	// Whether the deployment has started
	deploymentStarted bool
	// Keeps track of created resources
	displayedResources map[string]bool
	// Cache for display names, keyed by resource IDs
	resourceDisplayNames map[string]string
	// Tracks the number of times we've observed a terminal provisioning state for a given deployment operation,
	// keyed by operation ID
	terminalOperationPollCounts map[string]int
	// The last recorded spinner message, used to avoid unnecessary updates to the spinner
	lastSpinnerMessage string

	resourceManager ResourceManager
	console         input.Console
	deployment      Deployment
}

func NewProvisioningProgressDisplay(
	rm ResourceManager,
	console input.Console,
	deployment Deployment,
) *ProvisioningProgressDisplay {
	return &ProvisioningProgressDisplay{
		displayedResources:          map[string]bool{},
		resourceDisplayNames:        map[string]string{},
		terminalOperationPollCounts: map[string]int{},
		deployment:                  deployment,
		resourceManager:             rm,
		console:                     console,
	}
}

// getResourceTypeDisplayName returns the display name for a resource type, using a cache to avoid repeated lookups.
func (display *ProvisioningProgressDisplay) getResourceTypeDisplayName(
	ctx context.Context,
	resourceTypeName string,
	subscriptionId string,
	resourceId string,
) string {
	resourceType := azapi.AzureResourceType(resourceTypeName)
	// Check cache first
	if displayName, exists := display.resourceDisplayNames[resourceId]; exists {
		return displayName
	}

	// Try to get dynamic display name
	displayName, err := display.resourceManager.GetResourceTypeDisplayName(
		ctx,
		subscriptionId,
		resourceId,
		resourceType,
	)

	if err != nil {
		// Dynamic resource type translation failed -- fallback to static translation
		displayName = azapi.GetResourceTypeDisplayName(resourceType)
	}

	// Cache the result (even if empty)
	display.resourceDisplayNames[resourceId] = displayName
	return displayName
}

// ReportProgress reports the current deployment progress, setting the currently executing operation title and logging
// progress.
func (display *ProvisioningProgressDisplay) ReportProgress(
	ctx context.Context, queryStart *time.Time) error {
	if !display.deploymentStarted {
		_, err := display.deployment.Get(ctx)
		if err != nil {
			// Return default progress
			log.Printf("error while reporting progress: %v", err)
			return nil
		}

		display.deploymentStarted = true
		deploymentUrl, err := display.deployment.DeploymentUrl(ctx)
		if err != nil {
			return err
		}

		deploymentLink := fmt.Sprintf(output.WithLinkFormat("%s\n"), deploymentUrl)

		display.console.EnsureBlankLine(ctx)

		lines := []string{
			"You can view detailed progress in the Azure Portal:",
			deploymentLink,
		}

		if v, err := strconv.ParseBool(os.Getenv("AZD_DEMO_MODE")); err == nil && v {
			lines = []string{
				"You can view detailed progress in the Azure Portal.",
				"\n",
			}
		}

		display.console.MessageUxItem(
			ctx,
			&ux.MultilineMessage{
				Lines: lines,
			},
		)
	}

	newlyDeployedResources := []*armresources.DeploymentOperation{}
	newlyFailedResources := []*armresources.DeploymentOperation{}
	runningDeployments := []*armresources.DeploymentOperation{}

	err := display.resourceManager.WalkDeploymentOperations(ctx, display.deployment,
		func(ctx context.Context, operation *armresources.DeploymentOperation) error {
			if isNestedDeployment(operation) {
				if isTerminalProvisioningState(operation.Properties.ProvisioningState) {
					display.terminalOperationPollCounts[*operation.ID]++
					if display.terminalOperationPollCounts[*operation.ID] >= 2 {
						// we poll terminal operations twice to ensure we have properly seen it,
						// to avoid missing any resources that are created right at the end of the deployment operation
						return SkipExpand()
					}

				} else {
					// if the operation is observed in a non-terminal state again, clear its poll count
					delete(display.terminalOperationPollCounts, *operation.ID)
				}

				return nil
			}

			if operation.Properties.Timestamp == nil ||
				operation.Properties.ProvisioningOperation == nil ||
				operation.Properties.TargetResource == nil ||
				operation.Properties.TargetResource.ID == nil {
				return nil
			}

			// Build dedup key consistent with the key used when marking displayed
			lookupKey := *operation.Properties.TargetResource.ID
			if lookupKey == "" && operation.Properties.TargetResource.ResourceName != nil {
				lookupKey = *operation.Properties.TargetResource.ResourceName
			}

			if *operation.Properties.ProvisioningOperation == armresources.ProvisioningOperationCreate &&
				operation.Properties.Timestamp.After(*queryStart) &&
				!display.displayedResources[lookupKey] {
				switch *operation.Properties.ProvisioningState {
				case string(armresources.ProvisioningStateSucceeded):
					newlyDeployedResources = append(newlyDeployedResources, operation)
				case string(armresources.ProvisioningStateRunning):
					runningDeployments = append(runningDeployments, operation)
				case string(armresources.ProvisioningStateFailed):
					newlyFailedResources = append(newlyFailedResources, operation)
				}
			}

			return nil
		})
	if err != nil {
		// Status display is best-effort activity.
		return err
	}

	slices.SortFunc(newlyDeployedResources, func(
		a *armresources.DeploymentOperation,
		b *armresources.DeploymentOperation,
	) int {
		return a.Properties.Timestamp.Compare(*b.Properties.Timestamp)
	})

	displayedResources := append(newlyDeployedResources, newlyFailedResources...)
	display.logNewlyCreatedResources(ctx, displayedResources, runningDeployments)
	return nil
}
func (display *ProvisioningProgressDisplay) logNewlyCreatedResources(
	ctx context.Context,
	resources []*armresources.DeploymentOperation,
	inProgressResources []*armresources.DeploymentOperation,
) {
	for _, resource := range resources {
		resourceTypeName := *resource.Properties.TargetResource.ResourceType
		resourceTypeDisplayName := display.getResourceTypeDisplayName(
			ctx,
			resourceTypeName,
			display.deployment.SubscriptionId(),
			*resource.Properties.TargetResource.ID,
		)

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

		// Use ResourceID as the dedup key when available; fall back to ResourceName
		// if ResourceID is empty (e.g., during early provisioning when the ID is not yet assigned).
		dedupKey := *resource.Properties.TargetResource.ID
		if dedupKey == "" && resource.Properties.TargetResource.ResourceName != nil {
			dedupKey = *resource.Properties.TargetResource.ResourceName
		}

		display.displayedResources[dedupKey] = true
	}
	// update progress
	inProgress := []string{}
	for _, inProgResource := range inProgressResources {
		resourceTypeName := *inProgResource.Properties.TargetResource.ResourceType
		resourceTypeDisplayName := display.getResourceTypeDisplayName(
			ctx,
			resourceTypeName,
			display.deployment.SubscriptionId(),
			*inProgResource.Properties.TargetResource.ID,
		)

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

	// ensure stable ordering
	slices.Sort(inProgress)

	message := "Creating/Updating resources"
	if len(inProgress) > 0 {
		message = fmt.Sprintf("%s (%s)", message, strings.Join(inProgress, ", "))
	}

	// only update the spinner message if it has changed, to avoid updates which can cause flickering
	if message != display.lastSpinnerMessage {
		display.console.ShowSpinner(ctx, message, input.Step)
	}

	display.lastSpinnerMessage = message
}

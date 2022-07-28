package provisioning

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

const defaultProgressTitle string = "Creating Azure resources"
const succeededProvisioningState string = "Succeeded"

type ResourceManager interface {
	GetDeploymentResourceOperations(ctx context.Context, subscriptionId string, deploymentName string) ([]tools.AzCliResourceOperation, error)
	GetResourceTypeDisplayName(ctx context.Context, subscriptionId string, resourceId string, resourceType infra.AzureResourceType) (string, error)
	GetWebAppResourceTypeDisplayName(ctx context.Context, subscriptionId string, resourceId string) (string, error)
}

// ProvisioningProgressDisplay displays interactive progress for an ongoing Azure provisioning operation.
type ProvisioningProgressDisplay struct {
	// Keeps track of created resources
	createdResources map[string]bool
	subscriptionId   string
	deploymentName   string
	resourceManager  ResourceManager
}

func NewProvisioningProgressDisplay(rm ResourceManager, subscriptionId string, deploymentName string) ProvisioningProgressDisplay {
	return ProvisioningProgressDisplay{
		createdResources: map[string]bool{},
		subscriptionId:   subscriptionId,
		deploymentName:   deploymentName,
		resourceManager:  rm,
	}
}

// ReportProgress reports the current deployment progress, setting the currently executing operation title and logging progress.
func (display *ProvisioningProgressDisplay) ReportProgress(ctx context.Context, setOperationTitle func(string), logProgress func(string)) {
	operations, err := display.resourceManager.GetDeploymentResourceOperations(ctx, display.subscriptionId, display.deploymentName)
	if err != nil {
		// Status display is best-effort activity.
		return
	}

	succeededCount := 0
	newlyDeployedResources := []*tools.AzCliResourceOperation{}

	for i := range operations {
		if operations[i].Properties.ProvisioningState == succeededProvisioningState {
			succeededCount++

			if !display.createdResources[operations[i].Properties.TargetResource.Id] &&
				infra.IsTopLevelResourceType(infra.AzureResourceType(operations[i].Properties.TargetResource.ResourceType)) {
				newlyDeployedResources = append(newlyDeployedResources, &operations[i])
			}
		}
	}

	sort.Slice(newlyDeployedResources, func(i int, j int) bool {
		return time.Time.Before(newlyDeployedResources[i].Properties.Timestamp, newlyDeployedResources[j].Properties.Timestamp)
	})

	display.logNewlyCreatedResources(ctx, newlyDeployedResources, logProgress)

	status := ""

	if len(operations) > 0 {
		status = formatProgressTitle(succeededCount, len(operations))
	} else {
		status = defaultProgressTitle
	}

	setOperationTitle(status)
}

func (display *ProvisioningProgressDisplay) logNewlyCreatedResources(ctx context.Context, resources []*tools.AzCliResourceOperation, logProgress func(string)) {
	for _, newResource := range resources {
		resourceTypeName := newResource.Properties.TargetResource.ResourceType
		resourceTypeDisplayName, err := display.resourceManager.GetResourceTypeDisplayName(
			ctx, display.subscriptionId, newResource.Properties.TargetResource.Id, infra.AzureResourceType(resourceTypeName))

		if err != nil {
			// Dynamic resource type translation failed -- fallback to static translation
			resourceTypeDisplayName = infra.GetResourceTypeDisplayName(infra.AzureResourceType(resourceTypeName))
		}

		// Don't log resource types for Azure resources that we do not have a translation of the resource type for.
		// This will be improved on in a future iteration.
		if resourceTypeDisplayName != "" {
			logProgress(formatCreatedResourceLog(resourceTypeDisplayName, newResource.Properties.TargetResource.ResourceName))
			resourceTypeName = resourceTypeDisplayName
		}

		log.Printf(
			"%s - Created %s: %s",
			newResource.Properties.Timestamp.Local().Format("2006-01-02 15:04:05"),
			resourceTypeName,
			newResource.Properties.TargetResource.ResourceName)

		display.createdResources[newResource.Properties.TargetResource.Id] = true
	}
}

func formatCreatedResourceLog(resourceTypeDisplayName string, resourceName string) string {
	return fmt.Sprintf(
		"Created %s: %s",
		resourceTypeDisplayName,
		resourceName)
}

func formatProgressTitle(succeededCount int, totalCount int) string {
	return fmt.Sprintf("Creating Azure resources (%d of ~%d completed)", succeededCount, totalCount)
}

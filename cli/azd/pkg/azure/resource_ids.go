// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/convert"
)

// SubscriptionFromRID returns the subscription id component of a resource or panics if the resource id does not
// contain a subscription.
func SubscriptionFromRID(rid string) string {
	parts := strings.Split(rid, "/")
	for idx, part := range parts {
		if part == "subscriptions" && idx+1 < len(parts) {
			return parts[idx+1]
		}
	}

	panic(fmt.Sprintf("no subscription id component in in %s", rid))
}

// Creates Azure subscription resource ID
func SubscriptionRID(subscriptionId string) string {
	returnValue := fmt.Sprintf("/subscriptions/%s", subscriptionId)
	return returnValue
}

// Creates subscription-level deployment resource ID
func SubscriptionDeploymentRID(subscriptionId, deploymentId string) string {
	returnValue := fmt.Sprintf(
		"%s/providers/Microsoft.Resources/deployments/%s",
		SubscriptionRID(subscriptionId),
		deploymentId,
	)
	return returnValue
}

// Creates resource group level deployment resource ID
func ResourceGroupDeploymentRID(subscriptionId string, resourceGroupName string, deploymentId string) string {
	returnValue := fmt.Sprintf(
		"%s/providers/Microsoft.Resources/deployments/%s",
		ResourceGroupRID(subscriptionId, resourceGroupName),
		deploymentId,
	)
	return returnValue
}

// Creates resource ID for an Azure resource group
func ResourceGroupRID(subscriptionId, resourceGroupName string) string {
	returnValue := fmt.Sprintf("%s/resourceGroups/%s", SubscriptionRID(subscriptionId), resourceGroupName)
	return returnValue
}

func WebsiteRID(subscriptionId, resourceGroupName, websiteName string) string {
	returnValue := fmt.Sprintf(
		"%s/providers/Microsoft.Web/sites/%s",
		ResourceGroupRID(subscriptionId, resourceGroupName),
		websiteName,
	)
	return returnValue
}

func ContainerAppRID(subscriptionId, resourceGroupName, containerAppName string) string {
	returnValue := fmt.Sprintf(
		"%s/providers/Microsoft.App/containerApps/%s",
		ResourceGroupRID(subscriptionId, resourceGroupName),
		containerAppName,
	)
	return returnValue
}

func SpringAppRID(subscriptionId, resourceGroupName, springAppName string) string {
	returnValue := fmt.Sprintf(
		"%s/providers/Microsoft.AppPlatform/Spring/%s",
		ResourceGroupRID(subscriptionId, resourceGroupName),
		springAppName,
	)
	return returnValue
}

func KubernetesServiceRID(subscriptionId, resourceGroupName, clusterName string) string {
	return fmt.Sprintf(
		"%s/providers/Microsoft.ContainerService/managedClusters/%s",
		ResourceGroupRID(subscriptionId, resourceGroupName),
		clusterName,
	)
}

func StaticWebAppRID(subscriptionId, resourceGroupName, staticSiteName string) string {
	returnValue := fmt.Sprintf(
		"%s/providers/Microsoft.Web/staticSites/%s",
		ResourceGroupRID(subscriptionId, resourceGroupName),
		staticSiteName,
	)
	return returnValue
}

var resourceIdRegex = regexp.MustCompile("/.+/(?i)resourceGroups/(.+?)/.+")

// Find the resource group name from the resource id
func GetResourceGroupName(resourceId string) *string {
	matches := resourceIdRegex.FindSubmatch([]byte(resourceId))
	if matches == nil || len(matches) < 2 {
		return nil
	}

	return convert.RefOf(string(matches[1]))
}

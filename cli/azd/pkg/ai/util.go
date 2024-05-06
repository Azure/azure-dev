package ai

import (
	"fmt"
)

// AzureAiStudioLink returns a link to the Azure AI Studio for the given tenant, subscription, resource group, and workspace
func AzureAiStudioLink(tenantId string, subscriptionId string, resourceGroup string, workspaceName string) string {
	return fmt.Sprintf(
		//nolint:lll
		"https://ai.azure.com/build/overview?tid=%s&wsid=/subscriptions/%s/resourcegroups/%s/providers/Microsoft.MachineLearningServices/workspaces/%s",
		tenantId,
		subscriptionId,
		resourceGroup,
		workspaceName,
	)
}

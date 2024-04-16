package ai

import (
	"fmt"
)

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

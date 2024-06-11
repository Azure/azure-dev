package ai

import (
	"fmt"
)

// AiStudioWorkspaceLink returns a link to the Azure AI Studio workspace page
func AiStudioWorkspaceLink(tenantId string, subscriptionId string, resourceGroup string, workspaceName string) string {
	return fmt.Sprintf(
		//nolint:lll
		"https://ai.azure.com/build/overview?tid=%s&wsid=/subscriptions/%s/resourcegroups/%s/providers/Microsoft.MachineLearningServices/workspaces/%s",
		tenantId,
		subscriptionId,
		resourceGroup,
		workspaceName,
	)
}

// AzureAiStudioDeploymentLink returns a link to the Azure AI Studio deployment page
func AiStudioDeploymentLink(
	tenantId string,
	subscriptionId string,
	resourceGroup string,
	workspaceName string,
	endpointName string,
	deploymentName string,
) string {
	return fmt.Sprintf(
		//nolint:lll
		"https://ai.azure.com/projectdeployments/realtime/%s/%s/detail?wsid=/subscriptions/%s/resourceGroups/%s/providers/Microsoft.MachineLearningServices/workspaces/%s&tid=%s&deploymentName=%s",
		endpointName,
		deploymentName,
		subscriptionId,
		resourceGroup,
		workspaceName,
		tenantId,
		deploymentName,
	)
}

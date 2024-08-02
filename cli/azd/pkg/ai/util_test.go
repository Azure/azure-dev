package ai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_AiStudioLink(t *testing.T) {
	tenantId := "tenantId"
	subscriptionId := "subscriptionId"
	resourceGroup := "resourceGroup"
	workspaceName := "workspaceName"

	//nolint:lll
	expected := "https://ai.azure.com/build/overview?tid=tenantId&wsid=/subscriptions/subscriptionId/resourcegroups/resourceGroup/providers/Microsoft.MachineLearningServices/workspaces/workspaceName"
	actual := AiStudioWorkspaceLink(tenantId, subscriptionId, resourceGroup, workspaceName)

	require.Equal(t, expected, actual)
}

func Test_AiStudioDeploymentLink(t *testing.T) {
	tenantId := "tenantId"
	subscriptionId := "subscriptionId"
	resourceGroup := "resourceGroup"
	workspaceName := "workspaceName"
	endpointName := "endpointName"
	deploymentName := "deploymentName"

	//nolint:lll
	expected := "https://ai.azure.com/projectdeployments/realtime/endpointName/deploymentName/detail?wsid=/subscriptions/subscriptionId/resourceGroups/resourceGroup/providers/Microsoft.MachineLearningServices/workspaces/workspaceName&tid=tenantId&deploymentName=deploymentName"
	actual := AiStudioDeploymentLink(tenantId, subscriptionId, resourceGroup, workspaceName, endpointName, deploymentName)

	require.Equal(t, expected, actual)
}

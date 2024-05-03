package ai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_AzureAiStudioLink(t *testing.T) {
	tenantId := "tenantId"
	subscriptionId := "subscriptionId"
	resourceGroup := "resourceGroup"
	workspaceName := "workspaceName"

	//nolint:lll
	expected := "https://ai.azure.com/build/overview?tid=tenantId&wsid=/subscriptions/subscriptionId/resourcegroups/resourceGroup/providers/Microsoft.MachineLearningServices/workspaces/workspaceName"
	actual := AzureAiStudioLink(tenantId, subscriptionId, resourceGroup, workspaceName)

	require.Equal(t, expected, actual)
}

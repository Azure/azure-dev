package mockazcli

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
)

// NewAzCliFromMockContext creates a new instance of AzCli, configured to use the credential and pipeline from the
// provided mock context.
func NewAzCliFromMockContext(mockContext *mocks.MockContext) azcli.AzCli {
	return azcli.NewAzCli(
		mockaccount.SubscriptionCredentialProviderFunc(func(_ context.Context, _ string) (azcore.TokenCredential, error) {
			return mockContext.Credentials, nil
		}),
		mockContext.HttpClient,
		azcli.NewAzCliArgs{},
	)
}

func NewDeploymentOperationsServiceFromMockContext(
	mockContext *mocks.MockContext) azapi.DeploymentOperations {
	client, _ := armresources.NewDeploymentOperationsClient(
		"SUBSCRIPTION_ID", // TODO: this probably needs to be mocked
		mockContext.Credentials,
		mockContext.ArmClientOptions,
	)

	return azapi.NewDeploymentOperations(client)
}

func NewDeploymentsServiceFromMockContext(
	mockContext *mocks.MockContext) azapi.Deployments {
	client, _ := armresources.NewDeploymentsClient(
		"SUBSCRIPTION_ID", // TODO: this probably needs to be mocked
		mockContext.Credentials,
		mockContext.ArmClientOptions,
	)
	return azapi.NewDeployments(client)
}

package mockazapi

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/benbjohnson/clock"
)

// NewAzureClientFromMockContext creates a new instance of AzureClient, configured to use the credential and pipeline from
// the provided mock context.
func NewAzureClientFromMockContext(mockContext *mocks.MockContext) *azapi.AzureClient {
	return azapi.NewAzureClient(
		mockaccount.SubscriptionCredentialProviderFunc(func(_ context.Context, _ string) (azcore.TokenCredential, error) {
			return mockContext.Credentials, nil
		}),
		mockContext.ArmClientOptions,
	)
}

func NewDeploymentsServiceFromMockContext(
	mockContext *mocks.MockContext) azapi.DeploymentService {
	return azapi.NewStandardDeployments(
		mockaccount.SubscriptionCredentialProviderFunc(mockContext.SubscriptionCredentialProvider.CredentialForSubscription),
		mockContext.ArmClientOptions,
		azapi.NewResourceService(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions),
		cloud.AzurePublic(),
		clock.NewMock(),
	)
}

func NewStandardDeploymentsFromMockContext(
	mockContext *mocks.MockContext) *azapi.StandardDeployments {
	return azapi.NewStandardDeployments(
		mockaccount.SubscriptionCredentialProviderFunc(mockContext.SubscriptionCredentialProvider.CredentialForSubscription),
		mockContext.ArmClientOptions,
		azapi.NewResourceService(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions),
		cloud.AzurePublic(),
		clock.NewMock(),
	)
}

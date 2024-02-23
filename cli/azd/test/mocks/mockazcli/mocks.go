package mockazcli

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
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
		mockContext.ArmClientOptions,
	)
}

func NewDeploymentOperationsServiceFromMockContext(
	mockContext *mocks.MockContext) azapi.DeploymentOperations {
	return azapi.NewDeploymentOperations(
		mockaccount.SubscriptionCredentialProviderFunc(func(_ context.Context, _ string) (azcore.TokenCredential, error) {
			return mockContext.Credentials, nil
		}),
		mockContext.ArmClientOptions,
	)
}

func NewDeploymentsServiceFromMockContext(
	mockContext *mocks.MockContext) azapi.Deployments {
	return azapi.NewDeployments(
		mockaccount.SubscriptionCredentialProviderFunc(func(_ context.Context, _ string) (azcore.TokenCredential, error) {
			return mockContext.Credentials, nil
		}),
		mockContext.ArmClientOptions,
	)
}

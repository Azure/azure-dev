package mockazcli

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

// NewAzCliFromMockContext creates a new instance of AzCli, configured to use the credential and pipeline from the
// provided mock context.
func NewAzCliFromMockContext(mockContext *mocks.MockContext) azcli.AzCli {
	return azcli.NewAzCli(
		func(ctx context.Context, options *azcli.TokenCredentialProviderOptions) (azcore.TokenCredential, error) {
			return mockContext.CredentialProvider(ctx, nil)
		}, azcli.NewAzCliArgs{
			HttpClient: mockContext.HttpClient,
		})
}

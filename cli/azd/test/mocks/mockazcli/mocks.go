package mockazcli

import (
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

// NewAzCliFromMockContext creates a new instance of AzCli, configured to use the credential and pipeline from the
// provided mock context.
func NewAzCliFromMockContext(mockContext *mocks.MockContext) azcli.AzCli {
	return azcli.NewAzCli(mockContext.Credentials, azcli.NewAzCliArgs{
		HttpClient: mockContext.HttpClient,
	})
}

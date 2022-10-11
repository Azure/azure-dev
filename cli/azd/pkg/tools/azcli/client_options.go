package azcli

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

// Creates the ARM module client options for control plan operations
// These options include the underlying transport to be used.
func (cli *azCli) createArmClientOptions(ctx context.Context) *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			// Supports mocking for unit tests
			Transport: httputil.GetHttpClient(ctx),
			// Per request policies to inject into HTTP pipeline
			PerCallPolicies: []policy.Policy{
				NewUserAgentPolicy(cli.userAgent),
			},
		},
	}
}

// Creates the az core client options for data plane operations
// These options include the underlying transport to be used.
func (cli *azCli) createCoreClientOptions(ctx context.Context) *azcore.ClientOptions {
	return &azcore.ClientOptions{
		// Supports mocking for unit tests
		Transport: httputil.GetHttpClient(ctx),
		// Per request policies to inject into HTTP pipeline
		PerCallPolicies: []policy.Policy{
			NewUserAgentPolicy(cli.userAgent),
		},
	}
}

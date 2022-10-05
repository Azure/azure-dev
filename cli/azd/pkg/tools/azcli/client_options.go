package azcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

// Creates the ARM module client options for control plan operations
// These options include the underlying transport to be used.
func (cli *azCli) createArmClientOptions(ctx context.Context, apiVersion *string) *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			// Supports mocking for unit tests
			Transport: httputil.GetHttpClient(ctx),
			// Per request policies to inject into HTTP pipeline
			PerCallPolicies: []policy.Policy{
				NewUserAgentPolicy(cli.userAgent),
				NewApiVersionPolicy(apiVersion),
			},
		},
	}
}

// Creates the az core client options for data plane operations
// These options include the underlying transport to be used.
func (cli *azCli) createCoreClientOptions(ctx context.Context, apiVersion *string) *azcore.ClientOptions {
	return &azcore.ClientOptions{
		// Supports mocking for unit tests
		Transport: httputil.GetHttpClient(ctx),
		// Per request policies to inject into HTTP pipeline
		PerCallPolicies: []policy.Policy{
			NewUserAgentPolicy(cli.userAgent),
			NewApiVersionPolicy(apiVersion),
		},
	}
}

// Reads the raw HTTP response and attempt to convert it into the specified type
func readRawResponse[T any](response *http.Response) (*T, error) {
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}

	instance := new(T)

	err = json.Unmarshal(data, instance)
	if err != nil {
		return nil, fmt.Errorf("failed unmarshalling JSON from response: %w", err)
	}

	return instance, nil
}

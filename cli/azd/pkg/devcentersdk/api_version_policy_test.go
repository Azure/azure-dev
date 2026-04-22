// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package devcentersdk_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/stretchr/testify/require"
)

// captureTransport is a policy.Transporter that records the request it
// receives and returns a canned empty 200 response.
type captureTransport struct {
	lastRequest *http.Request
}

func (c *captureTransport) Do(req *http.Request) (*http.Response, error) {
	c.lastRequest = req
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       http.NoBody,
		Request:    req,
	}, nil
}

func runPolicyPipeline(t *testing.T, p policy.Policy) *http.Request {
	t.Helper()

	transport := &captureTransport{}
	clientOptions := &azcore.ClientOptions{
		Transport:       transport,
		PerCallPolicies: []policy.Policy{p},
	}

	pipeline := runtime.NewPipeline("test", "1.0.0", runtime.PipelineOptions{}, clientOptions)

	req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://example.com/resource")
	require.NoError(t, err)

	_, err = pipeline.Do(req)
	require.NoError(t, err)

	require.NotNil(t, transport.lastRequest)
	return transport.lastRequest
}

func TestApiVersionPolicy_InjectsDefault(t *testing.T) {
	t.Parallel()

	p := devcentersdk.NewApiVersionPolicy(nil)
	require.NotNil(t, p)

	sent := runPolicyPipeline(t, p)
	require.Equal(t, "2024-02-01", sent.URL.Query().Get("api-version"))
}

func TestApiVersionPolicy_PreservesExistingQueryParams(t *testing.T) {
	t.Parallel()

	p := devcentersdk.NewApiVersionPolicy(nil)

	transport := &captureTransport{}
	clientOptions := &azcore.ClientOptions{
		Transport:       transport,
		PerCallPolicies: []policy.Policy{p},
	}
	pipeline := runtime.NewPipeline("test", "1.0.0", runtime.PipelineOptions{}, clientOptions)

	req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://example.com/resource?foo=bar")
	require.NoError(t, err)

	_, err = pipeline.Do(req)
	require.NoError(t, err)

	q := transport.lastRequest.URL.Query()
	require.Equal(t, "bar", q.Get("foo"))
	require.Equal(t, "2024-02-01", q.Get("api-version"))
}

func TestApiVersionPolicy_CustomVersionStillBoundToDefault(t *testing.T) {
	// The current implementation always injects the default api-version even
	// when a custom value is provided. This test pins that documented behavior
	// so a future change will be intentional.
	t.Parallel()

	custom := "2023-10-01"
	p := devcentersdk.NewApiVersionPolicy(&custom)
	require.NotNil(t, p)

	sent := runPolicyPipeline(t, p)
	require.Equal(t, "2024-02-01", sent.URL.Query().Get("api-version"))
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// NewAgentClientForTest creates an AgentClient suitable for unit tests that use
// httptest.NewTLSServer. It uses the provided http.Client (which trusts the test
// server's self-signed certificate) and skips bearer token authentication.
func NewAgentClientForTest(endpoint string, httpClient *http.Client) *AgentClient {
	clientOptions := &policy.ClientOptions{
		Transport: &httpClientTransport{client: httpClient},
	}

	pipeline := runtime.NewPipeline(
		"azure-ai-agents-test",
		"v1.0.0",
		runtime.PipelineOptions{},
		clientOptions,
	)

	return &AgentClient{
		endpoint: endpoint,
		pipeline: pipeline,
	}
}

// httpClientTransport adapts an *http.Client to the policy.Transporter interface.
type httpClientTransport struct {
	client *http.Client
}

func (t *httpClientTransport) Do(req *http.Request) (*http.Response, error) {
	return t.client.Do(req)
}

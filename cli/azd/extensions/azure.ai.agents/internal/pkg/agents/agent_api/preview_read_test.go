// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"azureaiagent/internal/pkg/recordproxy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/require"
)

type previewTokenCredential struct {
	scopes []string
}

func (c *previewTokenCredential) GetToken(
	ctx context.Context, options policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	c.scopes = options.Scopes
	return fakeCredential{}.GetToken(ctx, options)
}

type previewRedirectTransport struct {
	requests []*http.Request
}

func (t *previewRedirectTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.requests = append(t.requests, request)
	response := &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody, Request: request}
	if len(t.requests) == 1 {
		response.StatusCode = http.StatusFound
		response.Header = http.Header{"Location": {"https://untrusted.example/agents/agent"}}
	}
	return response, nil
}

func TestPreviewTokenAudienceAndRedirectDestination(t *testing.T) {
	transport := &previewRedirectTransport{}
	original := recordproxy.Transport
	recordproxy.Transport = transport
	t.Cleanup(func() { recordproxy.Transport = original })
	credential := &previewTokenCredential{}
	client := NewAgentClient("https://account.services.ai.azure.com/api/projects/project", credential)
	_, err := client.GetAgent(t.Context(), "agent", AgentEndpointAPIVersion, false)
	require.Error(t, err)
	require.Equal(t, []string{"https://ai.azure.com/.default"}, credential.scopes)
	require.Len(t, transport.requests, 2)
	require.Equal(t, "account.services.ai.azure.com", transport.requests[0].URL.Hostname())
	require.NotEmpty(t, transport.requests[0].Header.Get("Authorization"))
	require.Equal(t, "untrusted.example", transport.requests[1].URL.Hostname())
	require.Empty(t, transport.requests[1].Header.Get("Authorization"))
}

func TestPreviewReadsLatestAgentWithGetOnly(t *testing.T) {
	for _, tc := range []struct {
		name       string
		definition string
		code       bool
	}{
		{name: "container", definition: `{
			"kind":"hosted","cpu":"0.5","memory":"1Gi",
			"container_configuration":{"image":"registry.example.com/agent:v1"}
		}`},
		{name: "code", code: true, definition: `{
			"kind":"hosted","cpu":"0.5","memory":"1Gi",
			"code_configuration":{"runtime":"python_3_13","entry_point":["python","app.py"]}
		}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &captureTransport{
				statusCode: http.StatusOK,
				body: `{"name":"agent","versions":{"latest":{"name":"agent","version":"7",` +
					`"metadata":{"tags":"[\"one\",\"two\"]","empty":""},"definition":` +
					tc.definition + `}}}`,
			}
			client := newTestClient("https://account.services.ai.azure.com/api/projects/project", transport)
			agent, err := client.GetAgent(t.Context(), "agent", AgentEndpointAPIVersion, false)
			require.NoError(t, err)
			require.Len(t, transport.requests, 1)
			require.Equal(t, http.MethodGet, transport.requests[0].Method)
			require.Equal(t, AgentEndpointAPIVersion, transport.requests[0].URL.Query().Get("api-version"))
			require.Equal(t, "/api/projects/project/agents/agent", transport.requests[0].URL.Path)
			require.Equal(t, "7", agent.Versions.Latest.Version)
			require.Equal(t, map[string]string{"tags": `["one","two"]`, "empty": ""}, agent.Versions.Latest.Metadata)
			data, err := json.Marshal(agent.Versions.Latest.Definition)
			require.NoError(t, err)
			var definition HostedAgentDefinition
			require.NoError(t, json.Unmarshal(data, &definition))
			require.Equal(t, AgentKindHosted, definition.Kind)
			require.Equal(t, tc.code, definition.CodeConfiguration != nil)
		})
	}
}

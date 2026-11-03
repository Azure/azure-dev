// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStandbyPoolManagementRequests(t *testing.T) {
	for _, operation := range []string{"create", "patch"} {
		for _, test := range []struct {
			name     string
			endpoint *AgentEndpoint
			version  string
			body     string
		}{
			{"omitted", nil, "v1", ""},
			{"empty endpoint", &AgentEndpoint{}, "v1", `{}`},
			{"positive", &AgentEndpoint{StandbyPool: &StandbyPool{Size: new(int32(3))}},
				StandbyPoolAPIVersion, `{"standby_pool":{"size":3}}`},
			{"zero", &AgentEndpoint{StandbyPool: &StandbyPool{Size: new(int32(0))}},
				StandbyPoolAPIVersion, `{"standby_pool":{"size":0}}`},
		} {
			t.Run(operation+"/"+test.name, func(t *testing.T) {
				transport := &captureTransport{statusCode: http.StatusOK,
					body: `{"name":"agent","agent_endpoint":{"standby_pool":{"size":0}}}`}
				client := newTestClient("https://example.test", transport)
				var agent *AgentObject
				var err error
				if operation == "create" {
					agent, err = client.CreateAgent(t.Context(), &CreateAgentRequest{
						Name: "agent", AgentEndpoint: test.endpoint,
						CreateAgentVersionRequest: CreateAgentVersionRequest{
							Definition: map[string]any{"kind": "hosted"},
						},
					}, AgentEndpointAPIVersion)
				} else {
					agent, err = client.PatchAgent(t.Context(), "agent",
						&PatchAgentRequest{AgentEndpoint: test.endpoint}, AgentEndpointAPIVersion)
				}
				require.NoError(t, err)
				require.Equal(t, int32(0), *agent.AgentEndpoint.StandbyPool.Size)
				require.Len(t, transport.requests, 1)
				request := transport.requests[0]
				require.Equal(t, test.version, request.URL.Query().Get("api-version"))
				require.Empty(t, request.Header.Get("Foundry-Features"))
				data, err := io.ReadAll(request.Body)
				require.NoError(t, err)
				var payload map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(data, &payload))
				if test.endpoint == nil {
					require.NotContains(t, payload, "agent_endpoint")
				} else {
					require.JSONEq(t, test.body, string(payload["agent_endpoint"]))
				}
				if operation == "patch" {
					require.Equal(t, http.MethodPatch, request.Method)
					require.NotContains(t, payload, "definition")
				} else {
					require.Equal(t, http.MethodPost, request.Method)
					require.NotContains(t, string(payload["definition"]), "standby_pool")
				}
			})
		}
	}
}

func TestStandbyPoolDoesNotChangeOtherAPIVersions(t *testing.T) {
	require.Equal(t, "custom-preview", endpointManagementAPIVersion(nil, "custom-preview"))
	require.Equal(t, "v1", endpointManagementAPIVersion(&AgentEndpoint{
		StandbyPool: &StandbyPool{},
	}, AgentEndpointAPIVersion))
	transport := &captureTransport{statusCode: http.StatusOK}
	client := newTestClient("https://example.test", transport)
	_, err := client.CreateAgentVersion(t.Context(), "agent", &CreateAgentVersionRequest{
		Definition: map[string]any{"kind": "hosted"},
	}, AgentEndpointAPIVersion)
	require.NoError(t, err)
	_, err = client.GetAgent(t.Context(), "agent", AgentEndpointAPIVersion, false)
	require.NoError(t, err)
	for _, request := range transport.requests {
		require.Equal(t, "v1", request.URL.Query().Get("api-version"))
	}
}

func TestStandbySessionStatusIsOpenString(t *testing.T) {
	for _, status := range []string{"standby", "future-state"} {
		var session AgentSessionResource
		require.NoError(t, json.Unmarshal([]byte(`{"id":"session","status":"`+status+`"}`), &session))
		require.Equal(t, AgentSessionStatus(status), session.Status)
	}
}

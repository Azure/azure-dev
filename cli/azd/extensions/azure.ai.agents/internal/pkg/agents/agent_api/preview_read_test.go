// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/require"
)

func TestPreviewGetAgentReadContract(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		body string
	}{
		{name: "existing", code: http.StatusOK, body: `{
			"name":"agent", "versions":{"latest":{"name":"agent","version":"3","definition":{
				"kind":"hosted","cpu":"0.5","memory":"1Gi",
				"container_configuration":{"image":"registry.example.com/agent:v1"}
			}}}
		}`},
		{name: "not found", code: http.StatusNotFound},
		{name: "unauthenticated", code: http.StatusUnauthorized},
		{name: "forbidden", code: http.StatusForbidden},
		{name: "malformed", code: http.StatusOK, body: `{"name":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, transport := newCaptureClient(tc.code, tc.body)
			agent, err := client.GetAgent(t.Context(), "agent", AgentEndpointAPIVersion, false)
			require.Len(t, transport.requests, 1)
			request := transport.requests[0]
			require.Equal(t, http.MethodGet, request.Method)
			require.Equal(t, "/api/projects/proj/agents/agent", request.URL.Path)
			require.Equal(t, AgentEndpointAPIVersion, request.URL.Query().Get("api-version"))
			require.Empty(t, request.Header.Get("Foundry-Features"))
			if tc.name == "existing" {
				require.NoError(t, err)
				require.Equal(t, "3", agent.Versions.Latest.Version)
				return
			}
			require.Error(t, err)
			require.Nil(t, agent)
			if tc.code != http.StatusOK {
				response, ok := errors.AsType[*azcore.ResponseError](err)
				require.True(t, ok)
				require.Equal(t, tc.code, response.StatusCode)
			}
		})
	}
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

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
				body: `{"name":"agent","versions":{"latest":{"name":"agent","version":"7","definition":` +
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
			data, err := json.Marshal(agent.Versions.Latest.Definition)
			require.NoError(t, err)
			var definition HostedAgentDefinition
			require.NoError(t, json.Unmarshal(data, &definition))
			require.Equal(t, AgentKindHosted, definition.Kind)
			require.Equal(t, tc.code, definition.CodeConfiguration != nil)
		})
	}
}

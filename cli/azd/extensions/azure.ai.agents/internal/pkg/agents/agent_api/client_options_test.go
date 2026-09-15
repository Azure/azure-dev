// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"bytes"
	"net/http"
	"testing"

	azcorelog "github.com/Azure/azure-sdk-for-go/sdk/azcore/log"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAgentClientWithOptionsDoesNotLogResponseBody(t *testing.T) {
	var logs bytes.Buffer
	azcorelog.SetListener(func(_ azcorelog.Event, message string) {
		logs.WriteString(message)
	})
	t.Cleanup(func() { azcorelog.SetListener(nil) })
	// #nosec G101 -- Synthetic URL credentials verify that response bodies are never logged.
	transport := &captureTransport{
		statusCode: http.StatusOK,
		body: `{"name":"agent","versions":{"latest":{"version":"1","definition":{
			"kind":"hosted","environment_variables":{"API_KEY":"private-secret"},
			"metadata":{"url":"https://secret-user:secret-password@example.com/path?sig=secret-token#secret-fragment"}
		}}}}`,
	}
	options := &policy.ClientOptions{Transport: transport}
	client := NewAgentClientWithOptions("https://example.com", fakeCredential{}, options)
	agent, err := client.GetAgent(t.Context(), "agent", AgentEndpointAPIVersion, true)
	require.NoError(t, err)
	require.NotNil(t, agent)
	require.Len(t, transport.requests, 1)
	assert.Equal(t, http.MethodGet, transport.requests[0].Method)
	assert.NotEmpty(t, transport.requests[0].Header.Get("Authorization"))
	assert.Contains(t, logs.String(), "GET")
	for _, secret := range []string{
		"private-secret", "secret-user", "secret-password", "secret-token", "secret-fragment",
	} {
		assert.NotContains(t, logs.String(), secret)
	}
	assert.Empty(t, options.PerCallPolicies, "constructor must not modify caller-owned options")
	assert.Empty(t, options.Logging.AllowedHeaders)
	assert.False(t, options.Logging.IncludeBody)
}

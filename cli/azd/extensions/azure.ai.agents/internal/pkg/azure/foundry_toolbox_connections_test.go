// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/require"
)

type toolboxTestCredential struct{}

func (toolboxTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestToolboxConnectionsClientProjectAndReplayableBody(t *testing.T) {
	t.Parallel()
	const projectID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/arm-name/projects/proj"
	const endpoint = "https://custom-subdomain.services.ai.azure.com/api/projects/proj"
	var payload []byte
	attempts := 0
	client, err := NewFoundryToolboxConnectionsClient(projectID, toolboxTestCredential{}, &policy.ClientOptions{
		Retry: policy.RetryOptions{MaxRetries: 1, RetryDelay: time.Millisecond},
		Transport: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			require.Equal(t, "Bearer test-token", req.Header.Get("Authorization"))
			status := http.StatusOK
			body := `{}`
			if !strings.Contains(req.URL.Path, "/connections/") {
				require.Equal(t, projectID, req.URL.Path)
				body = `{"properties":{"endpoints":{"AI Foundry API":"` + endpoint + `"}}}`
			} else if req.Method == http.MethodGet {
				status = http.StatusNotFound
			} else {
				attempts++
				b, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.NotEmpty(t, b)
				require.Equal(t, "application/json", req.Header.Get("Content-Type"))
				if attempts == 1 {
					payload = b
					status = http.StatusServiceUnavailable
				} else {
					require.Equal(t, payload, b)
				}
			}
			return &http.Response{StatusCode: status, Request: req, Body: io.NopCloser(strings.NewReader(body))}, nil
		})},
	})
	require.NoError(t, err)
	actual, err := client.ProjectEndpoint(t.Context())
	require.NoError(t, err)
	require.Equal(t, endpoint, actual)
	connection, err := client.GetConnection(t.Context(), "tools-mcp")
	require.NoError(t, err)
	require.Nil(t, connection)
	properties := ToolboxConnectionProperties{Category: "RemoteTool", AuthType: "UserEntraToken", Audience: "https://ai.azure.com",
		Target:   endpoint + "/toolboxes/tools/versions/1/mcp?api-version=v1",
		Metadata: map[string]string{"type": "toolbox_MCP", "toolboxName": "tools", "toolboxVersion": "1"}}
	require.NoError(t, client.PutConnection(t.Context(), "tools-mcp", properties))
	require.Equal(t, 2, attempts)
	var body toolboxConnectionBody
	require.NoError(t, json.NewDecoder(bytes.NewReader(payload)).Decode(&body))
	require.Equal(t, properties, body.Properties)
}

func TestToolboxConnectionsClientReturnsHTTPErrors(t *testing.T) {
	const projectID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj"
	client, err := NewFoundryToolboxConnectionsClient(projectID, toolboxTestCredential{}, &policy.ClientOptions{
		Transport: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusForbidden, Request: req,
				Body: io.NopCloser(strings.NewReader(`{"error":{"code":"AuthorizationFailed"}}`))}, nil
		})},
	})
	require.NoError(t, err)
	_, err = client.ProjectEndpoint(t.Context())
	require.Error(t, err)
	_, err = client.GetConnection(t.Context(), "tools")
	require.Error(t, err)
	err = client.PutConnection(t.Context(), "tools", ToolboxConnectionProperties{})
	require.Error(t, err)
}

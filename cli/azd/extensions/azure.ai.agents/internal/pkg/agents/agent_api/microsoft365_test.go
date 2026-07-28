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

func TestDownloadTeamsAppPackage_Success(t *testing.T) {
	const zipContent = "PK\x03\x04fake-zip-bytes"
	client, transport := newCaptureClient(http.StatusOK, zipContent)

	request := TeamsAppPackageRequest{
		BotServiceArmID:          "/subscriptions/s/resourceGroups/rg/providers/Microsoft.BotService/botServices/b",
		PublishScope:             "Personal",
		AgentDisplayName:         "my-agent",
		AppVersion:               "1.0.0",
		CanRespondWithoutMention: true,
	}

	zipBytes, err := client.DownloadTeamsAppPackage(t.Context(), "my-agent", request, Microsoft365APIVersion)
	require.NoError(t, err)
	require.Equal(t, zipContent, string(zipBytes))

	require.Len(t, transport.requests, 1)
	got := transport.requests[0]
	require.Equal(t, http.MethodPost, got.Method)
	require.Equal(t, "/api/projects/proj/agents/my-agent/microsoft365/zip", got.URL.Path)
	require.Equal(t, Microsoft365APIVersion, got.URL.Query().Get("api-version"))
	require.Equal(t, "application/zip", got.Header.Get("Accept"))
	require.Equal(t, "application/json", got.Header.Get("Content-Type"))
	require.Equal(t, "HostedAgents=V1Preview,AgentEndpoints=V1Preview", got.Header.Get("Foundry-Features"))

	bodyBytes, err := io.ReadAll(got.Body)
	require.NoError(t, err)
	var sent TeamsAppPackageRequest
	require.NoError(t, json.Unmarshal(bodyBytes, &sent))
	require.Equal(t, request, sent)
	require.False(t, sent.PublishAsAutopilot)
	require.False(t, sent.UseAgenticUserTemplate)
}

func TestDownloadTeamsAppPackage_ErrorStatus(t *testing.T) {
	client, _ := newCaptureClient(http.StatusForbidden, `{"error":{"code":"Forbidden","message":"nope"}}`)

	_, err := client.DownloadTeamsAppPackage(
		t.Context(), "my-agent", TeamsAppPackageRequest{}, Microsoft365APIVersion,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "403")
	require.Contains(t, err.Error(), "nope")
}

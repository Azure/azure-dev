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
		CanRespondWithoutMention: new(true),
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

func TestDownloadTeamsAppPackage_EmptyBody(t *testing.T) {
	client := newTestClient("https://example.test/api/projects/proj", &fakeTransport{statusCode: http.StatusOK})

	_, err := client.DownloadTeamsAppPackage(
		t.Context(), "my-agent", TeamsAppPackageRequest{}, Microsoft365APIVersion,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")
}

func TestPublishTeamsApp_Success(t *testing.T) {
	client, transport := newCaptureClient(
		http.StatusOK,
		`{"titleId":"T_123","teamsAppId":"app-456"}`,
	)

	request := TeamsAppPackageRequest{
		BotServiceArmID:          "/subscriptions/s/resourceGroups/rg/providers/Microsoft.BotService/botServices/b",
		PublishScope:             "Shared",
		AgentDisplayName:         "my-agent",
		AppVersion:               "1.0.0",
		CanRespondWithoutMention: new(true),
	}

	result, err := client.PublishTeamsApp(t.Context(), "my-agent", request, Microsoft365APIVersion)
	require.NoError(t, err)
	require.Equal(t, "T_123", result.TitleID)
	require.Equal(t, "app-456", result.TeamsAppID)

	require.Len(t, transport.requests, 1)
	got := transport.requests[0]
	require.Equal(t, http.MethodPost, got.Method)
	require.Equal(t, "/api/projects/proj/agents/my-agent/microsoft365/publish", got.URL.Path)
	require.Equal(t, Microsoft365APIVersion, got.URL.Query().Get("api-version"))
	require.Equal(t, "application/json", got.Header.Get("Content-Type"))
	require.Equal(t, "HostedAgents=V1Preview,AgentEndpoints=V1Preview", got.Header.Get("Foundry-Features"))

	bodyBytes, err := io.ReadAll(got.Body)
	require.NoError(t, err)
	var sent TeamsAppPackageRequest
	require.NoError(t, json.Unmarshal(bodyBytes, &sent))
	require.Equal(t, request, sent)
}

func TestPublishTeamsApp_DigitalWorkerRequest(t *testing.T) {
	client, transport := newCaptureClient(
		http.StatusOK,
		`{"titleId":"T_123","teamsAppId":"app-456"}`,
	)
	boundaries := []string{"read.1on1.developers", "write.group.developers"}
	request := TeamsAppPackageRequest{
		PublishAsAutopilot: true,
		OptionalPermissionScopes: []Microsoft365PermissionScopes{
			{ResourceAppID: "resource-app", Scopes: []string{"McpServers.Mail.All"}},
		},
		AccessBoundaries: &boundaries,
		PublishScope:     "Tenant",
		AgentDisplayName: "my-agent",
		AppVersion:       "1.0.0",
	}

	_, err := client.PublishTeamsApp(
		t.Context(), "my-agent", request, Microsoft365DigitalWorkerAPIVersion,
	)
	require.NoError(t, err)
	require.Len(t, transport.requests, 1)
	got := transport.requests[0]
	require.Equal(t, Microsoft365DigitalWorkerAPIVersion, got.URL.Query().Get("api-version"))
	require.Equal(
		t,
		microsoft365FeatureHeader+","+DigitalWorkerPreviewFeature,
		got.Header.Get("Foundry-Features"),
	)

	bodyBytes, err := io.ReadAll(got.Body)
	require.NoError(t, err)
	var sent TeamsAppPackageRequest
	require.NoError(t, json.Unmarshal(bodyBytes, &sent))
	require.Equal(t, request, sent)
	require.Empty(t, sent.BotServiceArmID)
	require.Equal(t, boundaries, *sent.AccessBoundaries)
	require.NotContains(t, string(bodyBytes), "useAgenticUserTemplate")
	require.NotContains(t, string(bodyBytes), "agenticUserTemplate")
}

func TestTeamsAppPackageRequest_AccessBoundaryTriState(t *testing.T) {
	omitted, err := json.Marshal(TeamsAppPackageRequest{})
	require.NoError(t, err)
	require.NotContains(t, string(omitted), "accessBoundaries")

	empty := []string{}
	cleared, err := json.Marshal(TeamsAppPackageRequest{AccessBoundaries: &empty})
	require.NoError(t, err)
	require.Contains(t, string(cleared), `"accessBoundaries":[]`)
}

func TestTeamsAppPackageRequest_CanRespondWithoutMentionTriState(t *testing.T) {
	omitted, err := json.Marshal(TeamsAppPackageRequest{})
	require.NoError(t, err)
	require.NotContains(t, string(omitted), "CanRespondWithoutMention")

	disabled := false
	configured, err := json.Marshal(TeamsAppPackageRequest{CanRespondWithoutMention: &disabled})
	require.NoError(t, err)
	require.Contains(t, string(configured), `"CanRespondWithoutMention":false`)
}

func TestPublishTeamsApp_ErrorStatus(t *testing.T) {
	client, _ := newCaptureClient(http.StatusForbidden, `{"error":{"code":"Forbidden","message":"no publish"}}`)

	_, err := client.PublishTeamsApp(
		t.Context(), "my-agent", TeamsAppPackageRequest{}, Microsoft365APIVersion,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "403")
	require.Contains(t, err.Error(), "no publish")
}

func TestPublishTeamsApp_EmptyBody(t *testing.T) {
	client := newTestClient("https://example.test/api/projects/proj", &fakeTransport{statusCode: http.StatusOK})

	_, err := client.PublishTeamsApp(
		t.Context(), "my-agent", TeamsAppPackageRequest{}, Microsoft365APIVersion,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")
}

func TestPublishTeamsApp_MissingTitleID(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"teamsAppId":"app-456"}`} {
		t.Run(body, func(t *testing.T) {
			client, _ := newCaptureClient(http.StatusOK, body)

			_, err := client.PublishTeamsApp(
				t.Context(), "my-agent", TeamsAppPackageRequest{}, Microsoft365APIVersion,
			)
			require.Error(t, err)
			require.Contains(t, err.Error(), "titleId")
		})
	}
}

func TestPublishTeamsApp_MissingTeamsAppID(t *testing.T) {
	client, _ := newCaptureClient(http.StatusOK, `{"titleId":"T_123"}`)

	_, err := client.PublishTeamsApp(
		t.Context(), "my-agent", TeamsAppPackageRequest{}, Microsoft365APIVersion,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "teamsAppId")
}

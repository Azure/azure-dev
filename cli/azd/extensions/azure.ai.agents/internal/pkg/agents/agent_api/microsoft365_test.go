// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDownloadTeamsAppPackage_Success(t *testing.T) {
	const zipContent = "PK\x03\x04fake-zip-bytes"
	reqCh := make(chan *http.Request, 1)
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		select {
		case reqCh <- r:
			bodyCh <- b
		default:
		}
		w.Header().Set("Content-Type", "application/zip")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(zipContent))
	}))
	defer server.Close()

	client := &AgentClient{
		endpoint:   server.URL,
		credential: fakeCredential{},
	}

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

	got := <-reqCh
	require.Equal(t, http.MethodPost, got.Method)
	require.Equal(t, "/agents/my-agent/microsoft365/zip", got.URL.Path)
	require.Equal(t, Microsoft365APIVersion, got.URL.Query().Get("api-version"))
	require.Equal(t, "application/zip", got.Header.Get("Accept"))
	require.Equal(t, "application/json", got.Header.Get("Content-Type"))
	require.Equal(t, "Bearer test-token", got.Header.Get("Authorization"))

	var sent TeamsAppPackageRequest
	require.NoError(t, json.Unmarshal(<-bodyCh, &sent))
	require.Equal(t, request, sent)
	require.False(t, sent.PublishAsAutopilot)
	require.False(t, sent.UseAgenticUserTemplate)
}

func TestDownloadTeamsAppPackage_ErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"Forbidden","message":"nope"}}`))
	}))
	defer server.Close()

	client := &AgentClient{
		endpoint:   server.URL,
		credential: fakeCredential{},
	}

	_, err := client.DownloadTeamsAppPackage(
		t.Context(), "my-agent", TeamsAppPackageRequest{}, Microsoft365APIVersion,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "403")
	require.Contains(t, err.Error(), "nope")
}

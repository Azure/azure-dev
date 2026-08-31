// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package experimenttracking

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type staticCredential struct{}

func (staticCredential) GetToken(
	context.Context,
	policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	return azcore.AccessToken{
		Token:     "test-token",
		ExpiresOn: time.Now().Add(time.Hour),
	}, nil
}

func TestNewClientDerivesProjectAndAccountIDs(t *testing.T) {
	client, err := newClient(
		"https://sample.services.ai.azure.com/api/projects/my%20project",
		"",
		"",
		staticCredential{},
		"",
		http.DefaultClient,
	)
	require.NoError(t, err)
	assert.Equal(t, "my project", client.ProjectID())
	assert.Equal(t, "sample", client.AccountID())
}

func TestNewClientUsesProjectIDOverride(t *testing.T) {
	client, err := newClient(
		"https://sample.services.ai.azure.com/custom/path",
		"override-project",
		"2026-01-01-preview",
		staticCredential{},
		"",
		http.DefaultClient,
	)
	require.NoError(t, err)
	assert.Equal(t, "override-project", client.ProjectID())
}

func TestDoJSONAddsAuthVersionAndRepeatedQueryValues(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		assert.Equal(t, "/api/projects/project/experiment_tracking/runs/run/system-metrics", r.URL.Path)
		assert.Equal(t, []string{"cpu", "memory"}, r.URL.Query()["names"])
		assert.Equal(t, "v1", r.URL.Query().Get("api-version"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(server.Close)

	client, err := newClient(
		server.URL+"/api/projects/project",
		"",
		"",
		staticCredential{},
		"",
		server.Client(),
	)
	require.NoError(t, err)

	response, err := client.DoJSON(
		t.Context(),
		http.MethodPost,
		"runs/run/system-metrics",
		map[string][]string{"names": {"cpu", "memory"}},
		nil,
		map[string]any{"value": true},
	)
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, string(response))
	assert.Equal(t, true, gotBody["value"])
}

func TestDoJSONReturnsResponseError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"code":"BadFilter","message":"invalid filter"}}`)
	}))
	t.Cleanup(server.Close)

	client, err := newClient(
		server.URL+"/api/projects/project",
		"",
		"",
		staticCredential{},
		"",
		server.Client(),
	)
	require.NoError(t, err)

	_, err = client.DoJSON(t.Context(), http.MethodGet, "runs", nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BadFilter")
}

func TestRunHeaders(t *testing.T) {
	client, err := newClient(
		"https://account.services.ai.azure.com/api/projects/project",
		"",
		"",
		staticCredential{},
		"",
		http.DefaultClient,
	)
	require.NoError(t, err)

	headers := client.RunHeaders("run-1")
	assert.Equal(t, "account", headers.Get("X-WANDB-USERNAME"))
	assert.Equal(t, "project", headers.Get("X-Helios-Project-Id"))
	assert.Equal(t, "run-1", headers.Get("x-helios-run-id"))
}

func TestNewClientRejectsInvalidDerivedProjectID(t *testing.T) {
	_, err := newClient(
		"https://account.services.ai.azure.com/api/projects/a/b",
		"",
		"",
		staticCredential{},
		"",
		http.DefaultClient,
	)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "exactly one project ID"))
}

func TestDoJSONUsesAPIKeyAuthentication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "", r.Header.Get("Authorization"))
		assert.Equal(t, "project-key", r.Header.Get("api-key"))
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(server.Close)

	client, err := newClient(
		server.URL+"/api/projects/project",
		"",
		"",
		nil,
		"project-key",
		server.Client(),
	)
	require.NoError(t, err)

	_, err = client.DoJSON(t.Context(), http.MethodGet, "runs", nil, nil, nil)
	require.NoError(t, err)
}

func TestDoJSONPreservesEscapedPathSegments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(
			t,
			"/api/projects/project/experiment_tracking/runs/run%20one/traces/trace%2Fone",
			r.URL.EscapedPath(),
		)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(server.Close)

	client, err := newClient(
		server.URL+"/api/projects/project",
		"",
		"",
		nil,
		"project-key",
		server.Client(),
	)
	require.NoError(t, err)

	_, err = client.DoJSON(
		t.Context(),
		http.MethodGet,
		"runs/run%20one/traces/trace%2Fone",
		nil,
		nil,
		nil,
	)
	require.NoError(t, err)
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package insights_api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCredential struct{}

func (fakeCredential) GetToken(
	context.Context,
	policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake-token"}, nil
}

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	pipeline := runtime.NewPipeline(
		"test",
		"v0.0.0",
		runtime.PipelineOptions{
			PerCall: []policy.Policy{foundryFeaturesPolicy{}},
		},
		&policy.ClientOptions{},
	)
	return NewClientFromPipeline(server.URL+"/api/projects/project", pipeline)
}

func TestNewClient(t *testing.T) {
	t.Parallel()

	client := NewClient("https://example.services.ai.azure.com/api/projects/project", fakeCredential{})

	require.NotNil(t, client)
	assert.Equal(t, "https://example.services.ai.azure.com/api/projects/project", client.endpoint)
}

func TestListMonitors(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/projects/project/agent_insight_monitors", r.URL.Path)
		assert.Equal(t, APIVersion, r.URL.Query().Get("api-version"))
		assert.Equal(t, "travel agent", r.URL.Query().Get("agent_name"))
		assert.Equal(t, "cursor-1", r.URL.Query().Get("after"))
		assert.Equal(t, "2", r.URL.Query().Get("limit"))
		assert.Equal(t, "desc", r.URL.Query().Get("order"))
		assert.Equal(t, insightsFeatureHeader, r.Header.Get("Foundry-Features"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{
				"id":         "monitor-1",
				"agent_name": "travel agent",
			}},
			"last_id":  "monitor-1",
			"has_more": false,
		})
	}))

	page, err := client.ListMonitors(t.Context(), "travel agent", "cursor-1", 2)

	require.NoError(t, err)
	require.Len(t, page.Data, 1)
	assert.Equal(t, "monitor-1", page.Data[0].ID)
}

func TestListInsightsPreservesUnknownFields(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/projects/project/agent_insight_monitors/monitor-1/insights", r.URL.Path)
		assert.Equal(t, APIVersion, r.URL.Query().Get("api-version"))
		assert.Equal(t, "aviation", r.URL.Query().Get("category"))
		assert.Equal(t, "high", r.URL.Query().Get("severity"))
		assert.Equal(t, "active", r.URL.Query().Get("status"))
		assert.Equal(t, "true", r.URL.Query().Get("include_details"))
		assert.Equal(t, "desc", r.URL.Query().Get("order"))
		assert.Equal(t, "cursor-1", r.URL.Query().Get("after"))
		assert.Equal(t, "100", r.URL.Query().Get("limit"))
		assert.Equal(t, insightsFeatureHeader, r.Header.Get("Foundry-Features"))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [{"id":"insight-1","future_field":{"preserved":true}}],
			"last_id":"insight-1",
			"has_more":false
		}`))
	}))

	page, err := client.ListInsights(t.Context(), "monitor-1", ListInsightOptions{
		Category:       "aviation",
		Severity:       "high",
		Status:         "active",
		IncludeDetails: true,
		Order:          "desc",
		After:          "cursor-1",
		Limit:          100,
	})

	require.NoError(t, err)
	require.Len(t, page.Data, 1)
	assert.JSONEq(t, `{"id":"insight-1","future_field":{"preserved":true}}`, string(page.Data[0]))
}

func TestListInsightsReturnsResponseError(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"Forbidden","message":"denied"}}`))
	}))

	_, err := client.ListInsights(t.Context(), "monitor-1", ListInsightOptions{})

	var responseError *azcore.ResponseError
	require.ErrorAs(t, err, &responseError)
	assert.Equal(t, http.StatusForbidden, responseError.StatusCode)
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package skill_api

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
	"github.com/stretchr/testify/require"
)

// fakeCredential is a no-op token credential for tests. It returns a static
// token without contacting any service.
type fakeCredential struct{}

func (fakeCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// newTestClient builds a Skills client rooted at the given httptest server URL.
// Uses the unexported insecure-http constructor so the bearer policy doesn't
// reject the plain-HTTP httptest endpoint.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return newClient(srv.URL, fakeCredential{}, "test", true)
}

func TestClient_CreateInline_SendsRequiredHeadersAndQuery(t *testing.T) {
	var capturedAPI string
	var capturedFeatures string
	var capturedContentType string
	var capturedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAPI = r.URL.Query().Get("api-version")
		capturedFeatures = r.Header.Get(FoundryFeaturesHeader)
		capturedContentType = r.Header.Get("Content-Type")
		require.Equal(t, "/skills", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &capturedBody))

		resp := skillWire{SkillID: "sk-1", Name: "my-skill", Description: "from-server"}
		w.Header().Set("Content-Type", ContentTypeJSON)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	skill, err := c.CreateInline(context.Background(), CreateRequest{
		Name:         "my-skill",
		Description:  "client-desc",
		Instructions: "client-body",
	})
	require.NoError(t, err)
	require.Equal(t, DataPlaneAPIVersion, capturedAPI)
	require.Equal(t, SkillsPreviewOptIn, capturedFeatures)
	require.Equal(t, ContentTypeJSON, capturedContentType)
	require.Equal(t, "my-skill", capturedBody["name"])
	require.Equal(t, "client-desc", capturedBody["description"])
	require.Equal(t, "client-body", capturedBody["instructions"])
	require.Equal(t, "sk-1", skill.SkillID)
	require.Equal(t, "from-server", skill.Description)
}

func TestClient_CreatePackage_SendsGzipContentType(t *testing.T) {
	var capturedCT string
	var capturedPath string
	var capturedBytes []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedCT = r.Header.Get("Content-Type")
		capturedBytes, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", ContentTypeJSON)
		_ = json.NewEncoder(w).Encode(skillWire{Name: "my-skill", HasBlob: true})
	}))
	defer srv.Close()

	payload := []byte("\x1f\x8b\x08\x00fake-gzip")
	c := newTestClient(t, srv)
	skill, err := c.CreatePackage(context.Background(), strings.NewReader(string(payload)), int64(len(payload)))
	require.NoError(t, err)
	require.Equal(t, "/skills:import", capturedPath)
	require.Equal(t, ContentTypeGzip, capturedCT)
	require.Equal(t, payload, capturedBytes)
	require.True(t, skill.HasBlob)
}

func TestClient_Get_DecodesSnakeCase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/skills/my-skill", r.URL.Path)
		require.Equal(t, SkillsPreviewOptIn, r.Header.Get(FoundryFeaturesHeader))
		_, _ = io.WriteString(w, `{"skill_id":"sk-1","name":"my-skill","has_blob":true,"description":"d"}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := c.Get(context.Background(), "my-skill")
	require.NoError(t, err)
	require.Equal(t, "sk-1", got.SkillID)
	require.True(t, got.HasBlob)
	require.Equal(t, "d", got.Description)
}

func TestClient_List_FlattensPagination(t *testing.T) {
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/skills", r.URL.Path)
		switch page {
		case 0:
			// First page: 2 items, has_more=true, last_id=b.
			require.Empty(t, r.URL.Query().Get("after"))
			_, _ = io.WriteString(w, `{"data":[{"name":"a"},{"name":"b"}],"has_more":true,"last_id":"b"}`)
		case 1:
			// Second page: cursor honored, 1 item, has_more=false.
			require.Equal(t, "b", r.URL.Query().Get("after"))
			_, _ = io.WriteString(w, `{"data":[{"name":"c"}],"has_more":false}`)
		default:
			t.Fatalf("unexpected extra page request: %d", page)
		}
		page++
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	all, err := c.ListAll(context.Background(), ListOptions{Top: 0}, 0)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "c"}, []string{all[0].Name, all[1].Name, all[2].Name})
}

func TestClient_List_HonorsLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "5", r.URL.Query().Get("limit"))
		require.Equal(t, "desc", r.URL.Query().Get("order"))
		_, _ = io.WriteString(w, `{"data":[{"name":"a"},{"name":"b"},{"name":"c"}],"has_more":true,"last_id":"c"}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	all, err := c.ListAll(context.Background(), ListOptions{Top: 5, OrderBy: "desc"}, 2)
	require.NoError(t, err)
	require.Len(t, all, 2)
}

func TestClient_Download_ValidatesContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/skills/my-skill:download", r.URL.Path)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "not gzip")
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.Download(context.Background(), "my-skill")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected download content type")
}

func TestClient_Download_PassesThroughGzipStream(t *testing.T) {
	payload := []byte("fake-gzip-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ContentTypeGzip)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	rc, err := c.Download(context.Background(), "my-skill")
	require.NoError(t, err)
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	require.Equal(t, payload, got)
}

func TestClient_Delete_ReturnsServiceResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/skills/my-skill", r.URL.Path)
		_, _ = io.WriteString(w, `{"name":"my-skill","deleted":true}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	resp, err := c.Delete(context.Background(), "my-skill")
	require.NoError(t, err)
	require.True(t, resp.Deleted)
	require.Equal(t, "my-skill", resp.Name)
}

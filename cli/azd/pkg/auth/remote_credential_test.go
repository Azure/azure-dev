// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/require"
)

func TestRemoteCredential(t *testing.T) {
	t.Parallel()

	fixedExpiry := time.Date(2030, 6, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		tenantID    string // struct-level tenant
		opts        policy.TokenRequestOptions
		status      int
		body        string
		wantToken   string
		wantExpiry  time.Time
		wantErr     bool
		errContains string
	}{
		{
			name:     "success returns token and expiry",
			tenantID: "my-tenant",
			opts: policy.TokenRequestOptions{
				Scopes: []string{"https://management.azure.com/.default"},
			},
			status: http.StatusOK,
			body: remoteCredTestJSON(map[string]any{
				"status": "success", "token": "tok-abc",
				"expiresOn": "2030-06-15T12:00:00Z",
			}),
			wantToken:  "tok-abc",
			wantExpiry: fixedExpiry,
		},
		{
			name: "error status returns code and message",
			opts: policy.TokenRequestOptions{
				Scopes: []string{"scope1"},
			},
			status: http.StatusOK,
			body: remoteCredTestJSON(
				map[string]any{"status": "error", "code": "auth_failed", "message": "bad creds"}),
			wantErr:     true,
			errContains: "bad creds",
		},
		{
			name: "non-200 HTTP status returns error with status code",
			opts: policy.TokenRequestOptions{
				Scopes: []string{"scope1"},
			},
			status:      http.StatusForbidden,
			body:        `{"error":"forbidden"}`,
			wantErr:     true,
			errContains: "unexpected status code",
		},
		{
			name: "malformed JSON returns decode error",
			opts: policy.TokenRequestOptions{
				Scopes: []string{"scope1"},
			},
			status:      http.StatusOK,
			body:        "<<<not json>>>",
			wantErr:     true,
			errContains: "decoding token response",
		},
		{
			name: "unexpected status field returns error",
			opts: policy.TokenRequestOptions{
				Scopes: []string{"scope1"},
			},
			status:      http.StatusOK,
			body:        remoteCredTestJSON(map[string]any{"status": "pending"}),
			wantErr:     true,
			errContains: "unexpected status",
		},
		{
			name: "empty scopes still succeeds",
			opts: policy.TokenRequestOptions{
				Scopes: []string{},
			},
			status: http.StatusOK,
			body: remoteCredTestJSON(map[string]any{
				"status": "success", "token": "empty-scope-tok",
				"expiresOn": "2030-06-15T12:00:00Z",
			}),
			wantToken:  "empty-scope-tok",
			wantExpiry: fixedExpiry,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer srv.Close()

			rc := newRemoteCredential(srv.URL, "test-key", tt.tenantID, srv.Client())
			tok, err := rc.GetToken(t.Context(), tt.opts)

			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errContains)
				require.Equal(t, azcore.AccessToken{}, tok)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantToken, tok.Token)
			require.True(t, tt.wantExpiry.Equal(tok.ExpiresOn),
				"expiry mismatch: want %v, got %v", tt.wantExpiry, tok.ExpiresOn)
		})
	}
}

// TestRemoteCredential_RequestFormat validates the HTTP method, URL path, query params, headers, and body.
func TestRemoteCredential_RequestFormat(t *testing.T) {
	t.Parallel()

	var captured struct {
		method      string
		path        string
		apiVersion  string
		contentType string
		authHeader  string
		body        []byte
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.apiVersion = r.URL.Query().Get("api-version")
		captured.contentType = r.Header.Get("Content-Type")
		captured.authHeader = r.Header.Get("Authorization")
		captured.body, _ = io.ReadAll(r.Body)

		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"success","token":"t","expiresOn":"2030-01-01T00:00:00Z"}`)
	}))
	defer srv.Close()

	rc := newRemoteCredential(srv.URL, "my-api-key", "the-tenant", srv.Client())
	_, err := rc.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes: []string{"https://graph.microsoft.com/.default", "openid"},
	})
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, captured.method)
	require.Equal(t, "/token", captured.path)
	require.Equal(t, "2023-07-12-preview", captured.apiVersion)
	require.Equal(t, "application/json", captured.contentType)
	require.Equal(t, "Bearer my-api-key", captured.authHeader)

	var reqBody struct {
		Scopes   []string `json:"scopes"`
		TenantId string   `json:"tenantId"`
	}
	require.NoError(t, json.Unmarshal(captured.body, &reqBody))
	require.Equal(t, []string{"https://graph.microsoft.com/.default", "openid"}, reqBody.Scopes)
	require.Equal(t, "the-tenant", reqBody.TenantId)
}

// TestRemoteCredential_TenantOverride verifies options.TenantID overrides the struct tenantID.
func TestRemoteCredential_TenantOverride(t *testing.T) {
	t.Parallel()

	var receivedTenant string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			TenantId string `json:"tenantId"`
		}
		_ = json.Unmarshal(body, &req)
		receivedTenant = req.TenantId

		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"success","token":"t","expiresOn":"2030-01-01T00:00:00Z"}`)
	}))
	defer srv.Close()

	rc := newRemoteCredential(srv.URL, "key", "default-tenant", srv.Client())
	_, err := rc.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes:   []string{"s1"},
		TenantID: "override-tenant",
	})
	require.NoError(t, err)
	require.Equal(t, "override-tenant", receivedTenant)
}

// TestRemoteCredential_TenantDefault verifies the struct tenantID is used when options.TenantID is empty.
func TestRemoteCredential_TenantDefault(t *testing.T) {
	t.Parallel()

	var receivedTenant string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			TenantId string `json:"tenantId"`
		}
		_ = json.Unmarshal(body, &req)
		receivedTenant = req.TenantId

		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"success","token":"t","expiresOn":"2030-01-01T00:00:00Z"}`)
	}))
	defer srv.Close()

	rc := newRemoteCredential(srv.URL, "key", "struct-tenant", srv.Client())
	_, err := rc.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes: []string{"s1"},
	})
	require.NoError(t, err)
	require.Equal(t, "struct-tenant", receivedTenant)
}

// TestRemoteCredential_ConnectionFailure verifies the error path when the server is unreachable.
func TestRemoteCredential_ConnectionFailure(t *testing.T) {
	t.Parallel()

	// Start a server then close it immediately so the port is unreachable.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	endpoint := srv.URL
	srv.Close()

	rc := newRemoteCredential(endpoint, "key", "", http.DefaultClient)
	_, err := rc.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes: []string{"scope1"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "making request")
}

// TestRemoteCredential_ContextCancelled verifies the request fails when the context is already cancelled.
func TestRemoteCredential_ContextCancelled(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"success","token":"t","expiresOn":"2030-01-01T00:00:00Z"}`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel before making the request

	rc := newRemoteCredential(srv.URL, "key", "", srv.Client())
	_, err := rc.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"scope1"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "RemoteCredential")
}

// TestRemoteCredential_NonOKStatusIncludesCode verifies the error message includes the HTTP status code.
func TestRemoteCredential_NonOKStatusIncludesCode(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable) // 503
	}))
	defer srv.Close()

	rc := newRemoteCredential(srv.URL, "key", "", srv.Client())
	_, err := rc.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes: []string{"scope1"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "503")
}

// TestRemoteCredential_ErrorResponseIncludesCode verifies the error response includes the code field.
func TestRemoteCredential_ErrorResponseIncludesCode(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"error","code":"token_expired","message":"token has expired"}`)
	}))
	defer srv.Close()

	rc := newRemoteCredential(srv.URL, "key", "", srv.Client())
	_, err := rc.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes: []string{"scope1"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "token_expired")
	require.Contains(t, err.Error(), "token has expired")
	require.Contains(t, err.Error(), "failed to acquire token")
}

// remoteCredTestJSON marshals v to JSON, panicking on error. Test helper only.
func remoteCredTestJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

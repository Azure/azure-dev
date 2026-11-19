// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type providerTestCredential struct{}

func (providerTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-azure-token"}, nil
}

func TestGetProviderConnection(t *testing.T) {
	tests := []struct {
		name        string
		credentials string
		status      int
		wantErr     string
	}{
		{"API key", `{"type":"ApiKey","key":"provider-test-key"}`, 200, ""},
		{"custom key", `{"type":"CustomKeys","Authorization":"Bearer provider-test-key"}`, 200, ""},
		{"unsupported", `{"type":"AAD"}`, 200, "require an ApiKey or CustomKeys"},
		{"missing key", `{"type":"ApiKey","key":""}`, 200, "does not contain an API key"},
		{"bad authorization", `{"type":"CustomKeys","Authorization":"Basic hidden"}`, 200, "Authorization Bearer"},
		{"forbidden", `{}`, 403, "read permission required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost ||
					r.URL.Path != "/api/projects/test/connections/fireworks/getConnectionWithCredentials" ||
					r.URL.Query().Get("api-version") != DefaultAPIVersion {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Authorization") != "Bearer test-azure-token" {
					t.Error("missing Azure authentication")
				}
				w.WriteHeader(tt.status)
				if tt.status != 200 {
					_, _ = w.Write([]byte(`{"error":{"code":"Forbidden","message":"read permission required"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"target":"https://api.fireworks.ai","credentials":` + tt.credentials + `}`))
			}))
			defer server.Close()
			c, err := NewFoundryClient(
				"https://test.services.ai.azure.com/api/projects/test", providerTestCredential{})
			if err != nil {
				t.Fatal(err)
			}
			c.baseURL, c.httpClient = server.URL, server.Client()
			connection, err := c.GetProviderConnection(t.Context(), "fireworks")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if connection.Target != "https://api.fireworks.ai" || connection.APIKey != "provider-test-key" {
				t.Fatal("incorrect connection endpoint or credential")
			}
			encoded, err := json.Marshal(connection)
			if err != nil || strings.Contains(string(encoded), "provider-test-key") {
				t.Fatal("connection serialization must not expose the API key")
			}
		})
	}
}

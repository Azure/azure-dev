// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package fireworks

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewClientRejectsUntrustedTargets(t *testing.T) {
	for _, target := range []string{
		"http://api.fireworks.ai", "https://api.fireworks.ai.evil.example",
		"https://api.fireworks.ai@evil.example", "https://user:secret@api.fireworks.ai",
		"https://api.fireworks.ai:8443", "https://api.fireworks.ai/v1",
		"https://api.fireworks.ai?key=secret", "https://api.fireworks.ai#secret", "://",
	} {
		t.Run(target, func(t *testing.T) {
			if _, err := NewClient(target, "provider-test-key"); err == nil {
				t.Fatal("accepted an untrusted connection target")
			} else if strings.Contains(err.Error(), "secret") {
				t.Fatal("error disclosed target credentials")
			}
		})
	}
}

func TestListModelsPagination(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/accounts/test-account/models" ||
			r.Header.Get("Authorization") != "Bearer provider-test-key" {
			t.Errorf("unexpected authenticated request: %s", r.URL.Path)
		}
		switch requests {
		case 1:
			_, _ = fmt.Fprint(w, `{"models":[{"name":"first","kind":"HF_BASE_MODEL"}],"nextPageToken":"a+/=&"}`)
		case 2:
			if r.URL.Query().Get("pageToken") != "a+/=&" {
				t.Error("pagination token was not preserved")
			}
			_, _ = fmt.Fprint(w, `{"models":[{"name":"second","state":"READY"}]}`)
		default:
			t.Error("unexpected extra request")
		}
	}))
	defer server.Close()
	c := &Client{endpoint: server.URL, apiKey: "provider-test-key", httpClient: server.Client()}
	models, err := c.ListModels(t.Context(), "test-account")
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(models) != 2 || models[1].Name != "second" {
		t.Fatalf("expected both listing pages, got %v", models)
	}
}

func TestListModelsFailures(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{"pagination cycle", 200, `{"nextPageToken":"again"}`, "repeated pagination token"},
		{"invalid JSON", 200, `{`, "failed to decode"},
		{"provider error", 403, `{"message":"API keys cannot download models"}`, "API keys cannot download models"},
		{"redacted error", 403,
			`{"message":"provider-test-key https://user:password@storage.example/model?sig=secret#fragment"}`,
			"https://storage.example/model"},
		{"non-JSON error", 502, "private response body", "Bad Gateway"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer server.Close()
			c := &Client{endpoint: server.URL, apiKey: "provider-test-key", httpClient: server.Client()}
			_, err := c.ListModels(t.Context(), "test-account")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
			for _, secret := range []string{"provider-test-key", "password", "sig=", "fragment", "private response body"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error disclosed %q", secret)
				}
			}
		})
	}
}

func TestListModelsDoesNotFollowRedirect(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("followed a redirect with provider credentials")
	}))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	c, err := NewClient("https://api.fireworks.ai", "provider-test-key")
	if err != nil {
		t.Fatal(err)
	}
	c.endpoint = server.URL
	if _, err := c.ListModels(t.Context(), "test-account"); err == nil {
		t.Fatal("redirect should fail")
	}
}

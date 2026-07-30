// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestBuildWorkspaceRoutePrefix_HappyPath verifies the helper produces the
// ARM-shaped path the vienna backend expects.
func TestBuildWorkspaceRoutePrefix_HappyPath(t *testing.T) {
	prefix, err := BuildWorkspaceRoutePrefix("sub-1", "rg-x", "ws-y")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "/agents/v2.0/subscriptions/sub-1/resourceGroups/rg-x/" +
		"providers/Microsoft.MachineLearningServices/workspaces/ws-y"
	if prefix != want {
		t.Errorf("prefix: got %q, want %q", prefix, want)
	}
}

// TestBuildWorkspaceRoutePrefix_RejectsMissingInputs covers each required arg.
func TestBuildWorkspaceRoutePrefix_RejectsMissingInputs(t *testing.T) {
	cases := map[string]struct{ sub, rg, ws string }{
		"missing sub": {"", "rg", "ws"},
		"missing rg":  {"sub", "", "ws"},
		"missing ws":  {"sub", "rg", ""},
		"all blank":   {" ", "\t", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := BuildWorkspaceRoutePrefix(tc.sub, tc.rg, tc.ws)
			if err == nil {
				t.Fatalf("expected error for %s", name)
			}
		})
	}
}

// TestSplitProjectEndpoint covers splitting a Foundry project data-plane
// endpoint into the client BaseURL and RoutePrefix, plus rejection of malformed
// inputs.
func TestSplitProjectEndpoint(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		base, prefix, err := SplitProjectEndpoint(
			"https://acct.services.ai.azure.com/api/projects/proj",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if base != "https://acct.services.ai.azure.com" {
			t.Errorf("base: got %q", base)
		}
		if prefix != "/api/projects/proj" {
			t.Errorf("prefix: got %q", prefix)
		}
		// The assembled agents route must match the documented contract.
		if got := base + prefix + "/agents"; got !=
			"https://acct.services.ai.azure.com/api/projects/proj/agents" {
			t.Errorf("agents URL: got %q", got)
		}
	})

	t.Run("trailing slash is tolerated", func(t *testing.T) {
		base, prefix, err := SplitProjectEndpoint(
			"https://acct.services.ai.azure.com/api/projects/proj/",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if base != "https://acct.services.ai.azure.com" || prefix != "/api/projects/proj" {
			t.Errorf("got base=%q prefix=%q", base, prefix)
		}
	})

	t.Run("rejects malformed inputs", func(t *testing.T) {
		for _, in := range []string{"", "   ", "not-a-url", "https://acct.services.ai.azure.com"} {
			if _, _, err := SplitProjectEndpoint(in); err == nil {
				t.Errorf("expected error for %q", in)
			}
		}
	})
}

// TestNewManagedAgentClient_RejectsBadOptions covers the construction-time
// validation surface so callers get actionable failures rather than nil
// dereferences inside operation calls.
func TestNewManagedAgentClient_RejectsBadOptions(t *testing.T) {
	cases := []struct {
		name        string
		opts        ManagedAgentClientOptions
		wantSubstr  string
		expectError bool
	}{
		{
			name:        "missing base URL",
			opts:        ManagedAgentClientOptions{RoutePrefix: "/agents/v2.0/x"},
			wantSubstr:  "BaseURL",
			expectError: true,
		},
		{
			name:        "missing route prefix",
			opts:        ManagedAgentClientOptions{BaseURL: "https://example.com"},
			wantSubstr:  "RoutePrefix",
			expectError: true,
		},
		{
			name: "route prefix missing leading slash",
			opts: ManagedAgentClientOptions{
				BaseURL:     "https://example.com",
				RoutePrefix: "agents/v2.0/x",
			},
			wantSubstr:  "/",
			expectError: true,
		},
		{
			name: "valid",
			opts: ManagedAgentClientOptions{
				BaseURL:     "http://localhost:5000",
				RoutePrefix: "/agents/v2.0/subscriptions/sub/resourceGroups/rg/providers/Microsoft.MachineLearningServices/workspaces/ws",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewManagedAgentClient(tc.opts)
			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error containing %q", tc.wantSubstr)
				}
				if !strings.Contains(err.Error(), tc.wantSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if client == nil {
				t.Fatal("expected non-nil client")
			}
		})
	}
}

// TestManagedAgentClient_CreateAgent_URLAndBody verifies that CreateAgent
// targets the expected ARM-rooted path, sets api-version, and forwards the
// JSON request body verbatim. Uses an httptest server in place of the real
// backend to keep the test hermetic.
func TestManagedAgentClient_CreateAgent_URLAndBody(t *testing.T) {
	var (
		gotPath   string
		gotMethod string
		gotQuery  string
		gotCT     string
		gotBody   []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotQuery = r.URL.RawQuery
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"object":"agent","id":"agt_1","name":"my-managed","versions":{"latest":{"object":"agent_version","id":"v1","name":"my-managed","version":"1"}}}`))
	}))
	defer srv.Close()

	prefix, err := BuildWorkspaceRoutePrefix("sub-1", "rg-x", "ws-y")
	if err != nil {
		t.Fatalf("prefix: %v", err)
	}
	client, err := NewManagedAgentClient(ManagedAgentClientOptions{
		BaseURL:     srv.URL,
		RoutePrefix: prefix,
	})
	if err != nil {
		t.Fatalf("NewManagedAgentClient: %v", err)
	}

	req := &CreateAgentRequest{
		Name: "my-managed",
		CreateAgentVersionRequest: CreateAgentVersionRequest{
			Definition: ManagedAgentDefinition{
				AgentDefinition: AgentDefinition{Kind: AgentKindManaged},
				Model:           "gpt-4.1-mini",
				Instructions:    "Be helpful.",
			},
		},
	}
	agent, err := client.CreateAgent(context.Background(), req, "2025-08-01-preview")
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if agent.Name != "my-managed" {
		t.Errorf("agent.Name: got %q, want %q", agent.Name, "my-managed")
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method: got %s, want POST", gotMethod)
	}
	wantPath := prefix + "/agents"
	if gotPath != wantPath {
		t.Errorf("path: got %q, want %q", gotPath, wantPath)
	}
	if gotQuery != "api-version=2025-08-01-preview" {
		t.Errorf("query: got %q, want %q", gotQuery, "api-version=2025-08-01-preview")
	}
	if gotCT != "application/json" {
		t.Errorf("content-type: got %q, want application/json", gotCT)
	}
	if !strings.Contains(string(gotBody), `"model":"gpt-4.1-mini"`) {
		t.Errorf("body should contain model field, got: %s", string(gotBody))
	}
	if !strings.Contains(string(gotBody), `"kind":"prompt"`) {
		t.Errorf("body should contain kind discriminator, got: %s", string(gotBody))
	}
}

// TestManagedAgentClient_DeleteAgent_URL verifies DELETE targets the expected
// path and threads the force flag into the query string.
func TestManagedAgentClient_DeleteAgent_URL(t *testing.T) {
	var (
		gotPath   string
		gotQuery  string
		gotMethod string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotMethod = r.Method
		_, _ = w.Write([]byte(`{"object":"agent.deleted","id":"agt_1","name":"my-managed","deleted":true}`))
	}))
	defer srv.Close()

	prefix, _ := BuildWorkspaceRoutePrefix("sub-1", "rg-x", "ws-y")
	client, err := NewManagedAgentClient(ManagedAgentClientOptions{
		BaseURL:     srv.URL,
		RoutePrefix: prefix,
	})
	if err != nil {
		t.Fatalf("NewManagedAgentClient: %v", err)
	}

	resp, err := client.DeleteAgent(context.Background(), "my-managed", "v1", true)
	if err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	if !resp.Deleted {
		t.Errorf("expected Deleted=true, got %+v", resp)
	}

	if gotMethod != http.MethodDelete {
		t.Errorf("method: got %s, want DELETE", gotMethod)
	}
	wantPath := prefix + "/agents/my-managed"
	if gotPath != wantPath {
		t.Errorf("path: got %q, want %q", gotPath, wantPath)
	}
	if !strings.Contains(gotQuery, "api-version=v1") {
		t.Errorf("query should contain api-version, got %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "force=true") {
		t.Errorf("query should contain force=true, got %q", gotQuery)
	}
}

// TestManagedAgentClient_CreateResponse_URL verifies that response creation
// targets the path-versioned /openai/v1/responses surface (no api-version
// query) and forwards the supplied JSON body and headers verbatim. The target
// agent travels in the body as `agent_reference`, not in the URL.
func TestManagedAgentClient_CreateResponse_URL(t *testing.T) {
	var (
		gotPath          string
		gotRawQuery      string
		gotMethod        string
		gotModelEndpoint string
		gotBody          []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRawQuery = r.URL.RawQuery
		gotMethod = r.Method
		gotModelEndpoint = r.Header.Get("x-model-endpoint")
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"id":"resp_1"}`))
	}))
	defer srv.Close()

	base, prefix, _ := SplitProjectEndpoint(srv.URL + "/api/projects/proj")
	client, _ := NewManagedAgentClient(ManagedAgentClientOptions{
		BaseURL:     base,
		RoutePrefix: prefix,
	})

	body, _, err := client.CreateResponse(
		context.Background(),
		[]byte(`{"input":"hello","agent_reference":{"type":"agent_reference","name":"my-managed"}}`),
		map[string]string{"x-model-endpoint": "https://aoai.example.com"},
	)
	if err != nil {
		t.Fatalf("CreateResponse: %v", err)
	}
	if !strings.Contains(string(body), "resp_1") {
		t.Errorf("body: %s", string(body))
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method: got %s, want POST", gotMethod)
	}
	wantPath := "/api/projects/proj/openai/v1/responses"
	if gotPath != wantPath {
		t.Errorf("path: got %q, want %q", gotPath, wantPath)
	}
	if gotRawQuery != "" {
		t.Errorf("query should be empty (path-versioned), got %q", gotRawQuery)
	}
	if gotModelEndpoint != "https://aoai.example.com" {
		t.Errorf("x-model-endpoint header: got %q", gotModelEndpoint)
	}
	if !strings.Contains(string(gotBody), `"agent_reference"`) {
		t.Errorf("body should forward agent_reference: %s", string(gotBody))
	}
}

// TestManagedAgentClient_DeleteAgent_NoForce verifies that when force=false
// is passed the `force` query parameter is omitted entirely (matches the
// vienna e2e contract — only api-version is sent).
func TestManagedAgentClient_DeleteAgent_NoForce(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	prefix, _ := BuildWorkspaceRoutePrefix("sub-1", "rg-x", "ws-y")
	client, _ := NewManagedAgentClient(ManagedAgentClientOptions{
		BaseURL:     srv.URL,
		RoutePrefix: prefix,
	})

	if _, err := client.DeleteAgent(context.Background(), "my-managed", "v1", false); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	if strings.Contains(gotQuery, "force") {
		t.Errorf("force should be omitted when false, got query %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "api-version=v1") {
		t.Errorf("query should retain api-version, got %q", gotQuery)
	}
}

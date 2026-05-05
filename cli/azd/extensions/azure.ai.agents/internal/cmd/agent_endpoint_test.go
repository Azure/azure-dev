// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
)

func TestParseAgentEndpoint(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantProj    string
		wantAgent   string
		wantProto   agent_api.AgentProtocol
		wantAPIVer  string
		wantErr     bool
		errContains string
	}{
		{
			name:       "invocations with api-version",
			raw:        "https://acct.services.ai.azure.com/api/projects/proj/agents/hello/endpoint/protocols/invocations?api-version=2025-11-15-preview",
			wantProj:   "https://acct.services.ai.azure.com/api/projects/proj",
			wantAgent:  "hello",
			wantProto:  agent_api.AgentProtocolInvocations,
			wantAPIVer: "2025-11-15-preview",
		},
		{
			name:      "invocations without api-version",
			raw:       "https://acct.services.ai.azure.com/api/projects/proj/agents/hello/endpoint/protocols/invocations",
			wantProj:  "https://acct.services.ai.azure.com/api/projects/proj",
			wantAgent: "hello",
			wantProto: agent_api.AgentProtocolInvocations,
		},
		{
			name:       "responses (openai/responses)",
			raw:        "https://acct.services.ai.azure.com/api/projects/proj/agents/echo/endpoint/protocols/openai/responses?api-version=2025-11-15-preview",
			wantProj:   "https://acct.services.ai.azure.com/api/projects/proj",
			wantAgent:  "echo",
			wantProto:  agent_api.AgentProtocolResponses,
			wantAPIVer: "2025-11-15-preview",
		},
		{
			name:      "trailing slash tolerated",
			raw:       "https://acct.services.ai.azure.com/api/projects/proj/agents/hello/endpoint/protocols/invocations/",
			wantProj:  "https://acct.services.ai.azure.com/api/projects/proj",
			wantAgent: "hello",
			wantProto: agent_api.AgentProtocolInvocations,
		},
		{
			name:        "empty url",
			raw:         "",
			wantErr:     true,
			errContains: "non-empty URL",
		},
		{
			name:        "http scheme rejected",
			raw:         "http://acct.services.ai.azure.com/api/projects/proj/agents/hello/endpoint/protocols/invocations",
			wantErr:     true,
			errContains: "https",
		},
		{
			name:        "non-foundry host rejected",
			raw:         "https://evil.com/api/projects/proj/agents/hello/endpoint/protocols/invocations",
			wantErr:     true,
			errContains: "Foundry host",
		},
		{
			name:        "host suffix injection rejected",
			raw:         "https://services.ai.azure.com.evil.com/api/projects/proj/agents/hello/endpoint/protocols/invocations",
			wantErr:     true,
			errContains: "Foundry host",
		},
		{
			name:        "missing api/projects prefix",
			raw:         "https://acct.services.ai.azure.com/agents/hello/endpoint/protocols/invocations",
			wantErr:     true,
			errContains: "path must match",
		},
		{
			name:        "unknown protocol tail",
			raw:         "https://acct.services.ai.azure.com/api/projects/proj/agents/hello/endpoint/protocols/grpc",
			wantErr:     true,
			errContains: "path must match",
		},
		{
			name:        "missing protocol tail",
			raw:         "https://acct.services.ai.azure.com/api/projects/proj/agents/hello/endpoint/protocols",
			wantErr:     true,
			errContains: "path must match",
		},
		{
			name:        "invalid agent name (chars)",
			raw:         "https://acct.services.ai.azure.com/api/projects/proj/agents/hel%20lo/endpoint/protocols/invocations",
			wantErr:     true,
			errContains: "agent name",
		},
		{
			name:        "malformed url",
			raw:         "https://%zz/foo",
			wantErr:     true,
			errContains: "invalid",
		},
		{
			name:        "explicit port rejected",
			raw:         "https://acct.services.ai.azure.com:444/api/projects/proj/agents/hello/endpoint/protocols/invocations",
			wantErr:     true,
			errContains: "must not include a port",
		},
		{
			name:        "empty api-version rejected",
			raw:         "https://acct.services.ai.azure.com/api/projects/proj/agents/hello/endpoint/protocols/invocations?api-version=",
			wantErr:     true,
			errContains: "api-version query parameter is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAgentEndpoint(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil; result=%+v", got)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.ProjectEndpoint != tt.wantProj {
				t.Errorf("ProjectEndpoint = %q, want %q", got.ProjectEndpoint, tt.wantProj)
			}
			if got.AgentName != tt.wantAgent {
				t.Errorf("AgentName = %q, want %q", got.AgentName, tt.wantAgent)
			}
			if got.Protocol != tt.wantProto {
				t.Errorf("Protocol = %q, want %q", got.Protocol, tt.wantProto)
			}
			if got.APIVersion != tt.wantAPIVer {
				t.Errorf("APIVersion = %q, want %q", got.APIVersion, tt.wantAPIVer)
			}
		})
	}
}

func TestIsValidAgentNameSegment(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"hello", true},
		{"hello-world", true},
		{"agent_v2", true},
		{"AGENT123", true},
		{"", false},
		{"hello world", false},
		{"hello/world", false},
		{"hello.world", false},
		{"agent@v1", false},
		{"agent:v1", false},
		{"agent*", false},
		{"agent!", false},
		{"héllo", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := isValidAgentNameSegment(tt.in); got != tt.want {
				t.Errorf("isValidAgentNameSegment(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestPrintEphemeralSessionHint(t *testing.T) {
	t.Run("no current sid, server returns sid -> hint", func(t *testing.T) {
		resp := &http.Response{Header: http.Header{"X-Agent-Session-Id": []string{"sess-123"}}}
		out := captureStdout(t, func() { printEphemeralSessionHint("", resp) })
		if !strings.Contains(out, "Server assigned session: sess-123") {
			t.Errorf("missing 'Server assigned session' line in output: %q", out)
		}
		if !strings.Contains(out, "--session-id sess-123") {
			t.Errorf("missing '--session-id sess-123' continuation hint in output: %q", out)
		}
	})
	t.Run("with existing sid -> no hint", func(t *testing.T) {
		resp := &http.Response{Header: http.Header{"X-Agent-Session-Id": []string{"sess-123"}}}
		out := captureStdout(t, func() { printEphemeralSessionHint("user-sid", resp) })
		if out != "" {
			t.Errorf("expected no output when caller already has a session id, got %q", out)
		}
	})
	t.Run("nil response -> no hint", func(t *testing.T) {
		out := captureStdout(t, func() { printEphemeralSessionHint("", nil) })
		if out != "" {
			t.Errorf("expected no output for nil response, got %q", out)
		}
	})
	t.Run("response without session header -> no hint", func(t *testing.T) {
		resp := &http.Response{Header: http.Header{}}
		out := captureStdout(t, func() { printEphemeralSessionHint("", resp) })
		if out != "" {
			t.Errorf("expected no output when server returns no session id, got %q", out)
		}
	})
	t.Run("via http test server", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("x-agent-session-id", "real-server-sid")
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()
		resp, err := http.Get(srv.URL) //nolint:gosec // test server
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out := captureStdout(t, func() { printEphemeralSessionHint("", resp) })
		if !strings.Contains(out, "real-server-sid") {
			t.Errorf("expected hint to include server-assigned sid, got %q", out)
		}
	})
}

func TestPrintEphemeralConversationHint(t *testing.T) {
	t.Run("auto-created conversation -> hint", func(t *testing.T) {
		out := captureStdout(t, func() { printEphemeralConversationHint("", "conv-abc") })
		if !strings.Contains(out, "--conversation-id conv-abc") {
			t.Errorf("missing '--conversation-id conv-abc' continuation hint in output: %q", out)
		}
	})
	t.Run("user supplied --conversation-id -> no hint", func(t *testing.T) {
		out := captureStdout(t, func() { printEphemeralConversationHint("user-conv", "conv-abc") })
		if out != "" {
			t.Errorf("expected no output when caller already has a conversation id, got %q", out)
		}
	})
	t.Run("no created conversation -> no hint", func(t *testing.T) {
		out := captureStdout(t, func() { printEphemeralConversationHint("", "") })
		if out != "" {
			t.Errorf("expected no output when no conversation id was created, got %q", out)
		}
	})
}

// captureStdout runs fn while redirecting os.Stdout and returns whatever was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	fn()
	_ = w.Close()
	return <-done
}

// TestBuildResponsesURL verifies that the responses URL builder uses the parsed
// api-version (rather than the default fallback) and URL-encodes it.
func TestBuildResponsesURL(t *testing.T) {
	parsed, err := parseAgentEndpoint(
		"https://acct.services.ai.azure.com/api/projects/proj/agents/echo/endpoint/protocols/openai/responses?api-version=2025-11-15-preview",
	)
	if err != nil {
		t.Fatalf("parseAgentEndpoint: %v", err)
	}
	got := buildResponsesURL(parsed.ProjectEndpoint, parsed.AgentName, parsed.APIVersion)
	want := "https://acct.services.ai.azure.com/api/projects/proj/agents/echo/endpoint/protocols/openai/responses?api-version=2025-11-15-preview"
	if got != want {
		t.Errorf("buildResponsesURL = %q, want %q", got, want)
	}

	// api-version must be query-escaped so unusual characters cannot break out.
	gotEscaped := buildResponsesURL("https://acct.services.ai.azure.com/api/projects/proj", "echo", "weird value&x=1")
	if !strings.Contains(gotEscaped, "api-version=weird+value%26x%3D1") {
		t.Errorf("buildResponsesURL did not escape api-version: %q", gotEscaped)
	}
}

// TestBuildInvocationsURL verifies that the invocations URL builder propagates
// the parsed api-version, URL-encodes it, and URL-encodes any session id.
func TestBuildInvocationsURL(t *testing.T) {
	parsed, err := parseAgentEndpoint(
		"https://acct.services.ai.azure.com/api/projects/proj/agents/hello/endpoint/protocols/invocations?api-version=2025-11-15-preview",
	)
	if err != nil {
		t.Fatalf("parseAgentEndpoint: %v", err)
	}

	t.Run("no session id", func(t *testing.T) {
		got := buildInvocationsURL(parsed.ProjectEndpoint, parsed.AgentName, parsed.APIVersion, "")
		want := "https://acct.services.ai.azure.com/api/projects/proj/agents/hello/endpoint/protocols/invocations?api-version=2025-11-15-preview"
		if got != want {
			t.Errorf("buildInvocationsURL = %q, want %q", got, want)
		}
	})

	t.Run("session id is escaped", func(t *testing.T) {
		got := buildInvocationsURL(parsed.ProjectEndpoint, parsed.AgentName, parsed.APIVersion, "a b/c?d&e")
		if !strings.Contains(got, "agent_session_id=a+b%2Fc%3Fd%26e") {
			t.Errorf("buildInvocationsURL did not escape session id: %q", got)
		}
	})

	t.Run("api-version is escaped", func(t *testing.T) {
		got := buildInvocationsURL("https://acct.services.ai.azure.com/api/projects/proj", "hello", "weird value&x=1", "")
		if !strings.Contains(got, "api-version=weird+value%26x%3D1") {
			t.Errorf("buildInvocationsURL did not escape api-version: %q", got)
		}
	})
}

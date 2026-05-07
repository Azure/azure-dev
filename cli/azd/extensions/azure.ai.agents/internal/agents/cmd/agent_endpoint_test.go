// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"azureaiagent/internal/agents/pkg/agents/agent_api"
)

func TestParseAgentEndpoint(t *testing.T) {
	t.Parallel()
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
			name:        "encoded slash in project segment rejected",
			raw:         "https://acct.services.ai.azure.com/api/projects/proj%2Fother/agents/hello/endpoint/protocols/invocations",
			wantErr:     true,
			errContains: "project segment is invalid",
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

// TestParseAgentEndpoint_RejectsInvalidAgentNames covers names that pass the
// regex's `[^/]+` capture but fail the canonical agent_yaml.ValidateAgentName
// check (which enforces the deployable-name format). Without this delegation
// these inputs would previously have been accepted locally and only failed
// later as 404s on the wire.
func TestParseAgentEndpoint_RejectsInvalidAgentNames(t *testing.T) {
	t.Parallel()
	cases := []string{
		// underscore — disallowed by the canonical validator
		"agent_v2",
		// 64 characters — exceeds the 63-char limit
		strings.Repeat("a", 64),
		// trailing hyphen — must end alphanumeric
		"agent-",
		// leading hyphen — must start alphanumeric
		"-agent",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			endpoint := "https://acct.services.ai.azure.com/api/projects/proj/agents/" +
				name + "/endpoint/protocols/invocations?api-version=2025-11-15-preview"
			_, err := parseAgentEndpoint(endpoint)
			if err == nil {
				t.Fatalf("parseAgentEndpoint(%q) = nil, want error", name)
			}
		})
	}
}

// TestBuildResponsesURL verifies that the responses URL builder uses the parsed
// api-version (rather than the default fallback) and URL-encodes it.
func TestBuildResponsesURL(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

// TestResolveRemoteContext_EphemeralMode exercises the ephemeral branch of
// resolveRemoteContext (--agent-endpoint path). It pins the api-version
// fallback (default applied when the URL omits the parameter) and the
// override (parsed value used when present), plus verifies that name,
// projectEndpoint, and agentKey are populated from the parsed endpoint.
//
// The project-mode branch is intentionally not covered here: it depends on
// the azd gRPC client and is exercised end-to-end by the functional/live
// tests in this PR's verification.
func TestResolveRemoteContext_EphemeralMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		raw            string
		wantAPIVersion string
		wantName       string
		wantProject    string
	}{
		{
			name: "api-version omitted falls back to default",
			raw: "https://acct.services.ai.azure.com/api/projects/proj/agents/" +
				"hello/endpoint/protocols/openai/responses",
			wantAPIVersion: DefaultAgentAPIVersion,
			wantName:       "hello",
			wantProject:    "https://acct.services.ai.azure.com/api/projects/proj",
		},
		{
			name: "explicit api-version overrides the default",
			raw: "https://acct.services.ai.azure.com/api/projects/proj/agents/" +
				"hello/endpoint/protocols/invocations?api-version=2025-09-01-preview",
			wantAPIVersion: "2025-09-01-preview",
			wantName:       "hello",
			wantProject:    "https://acct.services.ai.azure.com/api/projects/proj",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			parsed, err := parseAgentEndpoint(tc.raw)
			if err != nil {
				t.Fatalf("parseAgentEndpoint: %v", err)
			}
			a := &InvokeAction{flags: &invokeFlags{}, endpoint: parsed}

			rc, err := a.resolveRemoteContext(t.Context())
			if err != nil {
				t.Fatalf("resolveRemoteContext: %v", err)
			}
			if rc.azdClient != nil {
				defer rc.azdClient.Close()
			}

			if rc.apiVersion != tc.wantAPIVersion {
				t.Errorf("apiVersion = %q, want %q", rc.apiVersion, tc.wantAPIVersion)
			}
			if rc.name != tc.wantName {
				t.Errorf("name = %q, want %q", rc.name, tc.wantName)
			}
			if rc.projectEndpoint != tc.wantProject {
				t.Errorf("projectEndpoint = %q, want %q", rc.projectEndpoint, tc.wantProject)
			}
			if rc.agentKey == "" {
				t.Errorf("agentKey is empty; should be populated for ephemeral persistence")
			}
		})
	}
}

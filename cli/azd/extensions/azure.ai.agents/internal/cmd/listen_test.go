// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaiagent/internal/project"
)

func TestParseConnectionIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		json      string
		expected  map[string]string
		wantError bool
	}{
		{
			name:     "valid array",
			json:     `[{"name":"my-conn","id":"/subscriptions/123/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/ai/projects/proj/connections/my-conn"}]`,
			expected: map[string]string{"my-conn": "/subscriptions/123/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/ai/projects/proj/connections/my-conn"},
		},
		{
			name:     "empty string",
			json:     "",
			expected: map[string]string{},
		},
		{
			name:     "empty array",
			json:     "[]",
			expected: map[string]string{},
		},
		{
			name:      "invalid JSON",
			json:      "not-json",
			wantError: true,
		},
		{
			name: "multiple connections",
			json: `[{"name":"conn-a","id":"id-a"},{"name":"conn-b","id":"id-b"}]`,
			expected: map[string]string{
				"conn-a": "id-a",
				"conn-b": "id-b",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseConnectionIDs(tt.json)
			if tt.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(result) != len(tt.expected) {
				t.Fatalf("got %d entries, want %d", len(result), len(tt.expected))
			}
			for k, v := range tt.expected {
				if result[k] != v {
					t.Errorf("key %q: got %q, want %q", k, result[k], v)
				}
			}
		})
	}
}

func TestResolveToolboxConnectionIDs(t *testing.T) {
	t.Parallel()

	connIDs := map[string]string{
		"github_mcp_connection": "/subscriptions/123/connections/github_mcp_connection",
	}

	toolbox := project.Toolbox{
		Name: "test",
		Tools: []map[string]any{
			{"type": "web_search"},
			{"type": "mcp", "project_connection_id": "{{ github_mcp_connection }}"},
			{"type": "mcp", "project_connection_id": "unknown_conn"},
			{"type": "mcp", "project_connection_id": "github_mcp_connection"},
		},
	}

	resolveToolboxConnectionIDs(&toolbox, connIDs)

	// Tool without project_connection_id: unchanged
	if _, has := toolbox.Tools[0]["project_connection_id"]; has {
		t.Error("tool 0 should not have project_connection_id")
	}

	// Template ref {{ name }}: resolved to ARM ID
	if toolbox.Tools[1]["project_connection_id"] != "/subscriptions/123/connections/github_mcp_connection" {
		t.Errorf("tool 1 project_connection_id = %v, want ARM ID",
			toolbox.Tools[1]["project_connection_id"])
	}

	// Unknown connection: left as-is
	if toolbox.Tools[2]["project_connection_id"] != "unknown_conn" {
		t.Errorf("tool 2 project_connection_id = %v, want 'unknown_conn'",
			toolbox.Tools[2]["project_connection_id"])
	}

	// Bare name (no braces): also resolved
	if toolbox.Tools[3]["project_connection_id"] != "/subscriptions/123/connections/github_mcp_connection" {
		t.Errorf("tool 3 project_connection_id = %v, want ARM ID",
			toolbox.Tools[3]["project_connection_id"])
	}
}

func TestResolveTemplateRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"{{ my_conn }}", "my_conn"},
		{"{{my_conn}}", "my_conn"},
		{"{{  spaced  }}", "spaced"},
		{"my_conn", "my_conn"},
		{"", ""},
		{"{not_template}", "{not_template}"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			if got := resolveTemplateRef(tt.input); got != tt.want {
				t.Errorf("resolveTemplateRef(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildConnectionCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		connections []project.Connection
		wantKeys    []string
		wantEmpty   bool
	}{
		{
			name:      "empty connections",
			wantEmpty: true,
		},
		{
			name: "connections with credentials",
			connections: []project.Connection{
				{
					Name:        "my-openai",
					Credentials: map[string]any{"key": "${OPENAI_API_KEY}"},
				},
				{
					Name:        "github-mcp",
					Credentials: map[string]any{"pat": "${GITHUB_PAT}"},
				},
			},
			wantKeys: []string{"my-openai", "github-mcp"},
		},
		{
			name: "skips connections without credentials",
			connections: []project.Connection{
				{
					Name:        "no-creds",
					Credentials: nil,
				},
				{
					Name:        "has-creds",
					Credentials: map[string]any{"secret": "val"},
				},
			},
			wantKeys: []string{"has-creds"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := buildConnectionCredentials(tt.connections)

			if tt.wantEmpty {
				if len(result) != 0 {
					t.Fatalf("expected empty map, got %v", result)
				}
				return
			}

			if len(result) != len(tt.wantKeys) {
				t.Fatalf("expected %d entries, got %d: %v",
					len(tt.wantKeys), len(result), result)
			}

			for _, key := range tt.wantKeys {
				if _, ok := result[key]; !ok {
					t.Errorf("expected key %q in result", key)
				}
			}
		})
	}
}

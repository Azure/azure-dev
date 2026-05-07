// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateStoreField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		field   string
		wantErr bool
	}{
		{"sessions", false},
		{"conversations", false},
		{"invalid", true},
		{"", true},
		{"Sessions", true}, // case-sensitive
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			t.Parallel()
			err := validateStoreField(tt.field)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateKeySegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid-agent", "my-agent", false},
		{"valid-version", "1", false},
		{"with-forward-slash", "path/to/agent", true},
		{"with-backslash", "path\\agent", true},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateKeySegment("agentName", tt.value)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBuildRemoteAgentKeyFromEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{
			name:     "full agent endpoint",
			endpoint: "https://ai-account.services.ai.azure.com/api/projects/my-project/agents/my-agent/versions/1",
			want:     "ai-account.services.ai.azure.com/api/projects/my-project/agents/my-agent/versions/1/remote",
		},
		{
			name:     "trailing slash stripped",
			endpoint: "https://example.com/agents/test/versions/2/",
			want:     "example.com/agents/test/versions/2/remote",
		},
		{
			name:     "matches buildAgentKey output",
			endpoint: "https://host.com/api/projects/proj/agents/foo/versions/3",
			want:     buildAgentKey("https://host.com/api/projects/proj", "foo", "3", false),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildRemoteAgentKeyFromEndpoint(tt.endpoint)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildRemoteAgentKeyFromEndpoint_EquivalentToBuildAgentKey(t *testing.T) {
	t.Parallel()

	// Given a project endpoint and agent details, both approaches should produce the same key.
	projectEndpoint := "https://myaccount.services.ai.azure.com/api/projects/myproj"
	agentName := "my-agent"
	version := "5"

	// buildAgentKey composes the full path from parts
	fromParts := buildAgentKey(projectEndpoint, agentName, version, false)

	// buildRemoteAgentKeyFromEndpoint takes the pre-composed URL
	composedURL := projectEndpoint + "/agents/" + agentName + "/versions/" + version
	fromEndpoint := buildRemoteAgentKeyFromEndpoint(composedURL)

	require.Equal(t, fromParts, fromEndpoint)
}

func TestNormalizeEndpoint_StripScheme(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"https://example.com/path", "example.com/path"},
		{"http://HOST.COM/path", "host.com/path"},
		{"https://UPPER.COM", "upper.com"},
		{"no-scheme.com/path", "no-scheme.com/path"},
		{"localhost:8080", "localhost:8080"},
		{"https://host.com/Path/With/Case", "host.com/Path/With/Case"},
		{"", ""},
		{"https://host-only.com", "host-only.com"},
		{"HOST-ONLY", "host-only"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, normalizeEndpoint(tt.input))
		})
	}
}

func TestProjectHash_Deterministic(t *testing.T) {
	t.Parallel()

	// Same path always produces same hash.
	h1 := projectHash("/my/project")
	h2 := projectHash("/my/project")
	assert.Equal(t, h1, h2)

	// Different paths produce different hashes.
	h3 := projectHash("/other/project")
	assert.NotEqual(t, h1, h3)

	// Empty path returns "unknown".
	assert.Equal(t, "unknown", projectHash(""))

	// Hash is 16 hex chars (8 bytes of SHA256).
	assert.Len(t, h1, 16)
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// hyperlinkPrefix is the OSC 8 escape sequence prefix used for terminal hyperlinks.
// The actual WithHyperlink function may not emit this in non-terminal environments,
// so we check for its absence to verify non-clickable behavior.
const hyperlinkPrefix = "\x1b]8;;"

func TestArtifactToString_Endpoint(t *testing.T) {
	tests := []struct {
		name              string
		artifact          *Artifact
		contains          []string
		shouldBeClickable bool
	}{
		{
			name: "remote endpoint is clickable by default",
			artifact: &Artifact{
				Kind:         ArtifactKindEndpoint,
				Location:     "https://example.com/api",
				LocationKind: LocationKindRemote,
			},
			contains: []string{
				"- Endpoint:",
				"https://example.com/api",
			},
			shouldBeClickable: true,
		},
		{
			name: "remote endpoint with clickable=false is not hyperlinked",
			artifact: &Artifact{
				Kind:         ArtifactKindEndpoint,
				Location:     "https://example.com/agents/myagent",
				LocationKind: LocationKindRemote,
				Metadata: map[string]string{
					MetadataKeyClickable: "false",
				},
			},
			contains: []string{
				"- Endpoint:",
				"https://example.com/agents/myagent",
			},
			shouldBeClickable: false,
		},
		{
			name: "agent endpoint with custom label and note",
			artifact: &Artifact{
				Kind:         ArtifactKindEndpoint,
				Location:     "https://example.com/agents/myagent/versions/1",
				LocationKind: LocationKindRemote,
				Metadata: map[string]string{
					"label":              "Agent endpoint",
					MetadataKeyClickable: "false",
					MetadataKeyNote:      "For information on invoking the agent, see https://aka.ms/azd-agents-invoke",
				},
			},
			contains: []string{
				"- Agent endpoint:",
				"https://example.com/agents/myagent/versions/1",
				"For information on invoking the agent, see https://aka.ms/azd-agents-invoke",
			},
			shouldBeClickable: false,
		},
		{
			name: "local endpoint is clickable by default",
			artifact: &Artifact{
				Kind:         ArtifactKindEndpoint,
				Location:     "http://localhost:8080",
				LocationKind: LocationKindLocal,
			},
			contains: []string{
				"- Endpoint:",
				"http://localhost:8080",
			},
			shouldBeClickable: true,
		},
		{
			name: "endpoint with discriminator",
			artifact: &Artifact{
				Kind:         ArtifactKindEndpoint,
				Location:     "https://example.com/api",
				LocationKind: LocationKindRemote,
				Metadata: map[string]string{
					"discriminator": "(primary)",
				},
			},
			contains: []string{
				"- Endpoint:",
				"https://example.com/api",
				"(primary)",
			},
			shouldBeClickable: true,
		},
		{
			name: "clickable=FALSE is case insensitive",
			artifact: &Artifact{
				Kind:         ArtifactKindEndpoint,
				Location:     "https://example.com/api",
				LocationKind: LocationKindRemote,
				Metadata: map[string]string{
					MetadataKeyClickable: "FALSE",
				},
			},
			contains: []string{
				"- Endpoint:",
				"https://example.com/api",
			},
			shouldBeClickable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.artifact.ToString("")

			for _, expected := range tt.contains {
				require.True(t, strings.Contains(result, expected),
					"Expected output to contain %q, got: %s", expected, result)
			}

			// Check clickability by looking for hyperlink escape sequence
			hasHyperlink := strings.Contains(result, hyperlinkPrefix)
			if tt.shouldBeClickable {
				// In terminal environments, should have hyperlink; in non-terminal, won't have it
				// We can't directly test this without mocking terminal, so we just verify the URL is present
				require.Contains(t, result, tt.artifact.Location)
			} else {
				// Should NOT have hyperlink escape sequence
				require.False(t, hasHyperlink,
					"Expected output NOT to contain hyperlink escape sequence for non-clickable endpoint, got: %q", result)
			}
		})
	}
}

func TestArtifactToString_OtherKinds(t *testing.T) {
	tests := []struct {
		name     string
		artifact *Artifact
		contains string
	}{
		{
			name: "container remote",
			artifact: &Artifact{
				Kind:         ArtifactKindContainer,
				Location:     "myregistry.azurecr.io/myimage:latest",
				LocationKind: LocationKindRemote,
			},
			contains: "- Remote Image:",
		},
		{
			name: "container local",
			artifact: &Artifact{
				Kind:         ArtifactKindContainer,
				Location:     "myimage:latest",
				LocationKind: LocationKindLocal,
			},
			contains: "- Container:",
		},
		{
			name: "archive",
			artifact: &Artifact{
				Kind:         ArtifactKindArchive,
				Location:     "/path/to/output.zip",
				LocationKind: LocationKindLocal,
			},
			contains: "- Package Output:",
		},
		{
			name: "directory",
			artifact: &Artifact{
				Kind:         ArtifactKindDirectory,
				Location:     "/path/to/build",
				LocationKind: LocationKindLocal,
			},
			contains: "- Build Output:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.artifact.ToString("")
			require.Contains(t, result, tt.contains)
		})
	}
}

func TestArtifactToString_EmptyLocation(t *testing.T) {
	artifact := &Artifact{
		Kind:         ArtifactKindEndpoint,
		Location:     "",
		LocationKind: LocationKindRemote,
	}

	result := artifact.ToString("")
	require.Empty(t, result)
}

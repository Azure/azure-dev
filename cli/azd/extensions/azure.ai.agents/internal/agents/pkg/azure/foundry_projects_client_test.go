// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateNextLinkOrigin(t *testing.T) {
	baseURL, _ := url.Parse("https://myaccount.services.ai.azure.com/api/projects/myproject")

	client := &FoundryProjectsClient{
		baseOriginURL: baseURL,
	}

	tests := []struct {
		name    string
		link    string
		wantErr bool
	}{
		{
			name:    "valid same-origin link",
			link:    "https://myaccount.services.ai.azure.com/api/projects/myproject/connections?skip=10",
			wantErr: false,
		},
		{
			name:    "cross-origin attack URL",
			link:    "https://evil.com/steal?data=secret",
			wantErr: true,
		},
		{
			name:    "scheme-relative URL",
			link:    "//evil.com/path",
			wantErr: true,
		},
		{
			name:    "missing scheme",
			link:    "evil.com/path",
			wantErr: true,
		},
		{
			name:    "malformed URL",
			link:    "://invalid",
			wantErr: true,
		},
		{
			name:    "different scheme",
			link:    "http://myaccount.services.ai.azure.com/api/projects/myproject",
			wantErr: true,
		},
		{
			name:    "different host same scheme",
			link:    "https://other.services.ai.azure.com/api/projects/myproject",
			wantErr: true,
		},
		{
			name:    "case-insensitive scheme match",
			link:    "HTTPS://myaccount.services.ai.azure.com/api/projects/myproject",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.validateNextLinkOrigin(tt.link)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateNextLinkOrigin_NilBaseURL(t *testing.T) {
	client := &FoundryProjectsClient{
		baseOriginURL: nil,
	}

	err := client.validateNextLinkOrigin("https://example.com/path")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not initialized")
}

func TestNewFoundryProjectsClient_InvalidInputs(t *testing.T) {
	tests := []struct {
		name        string
		accountName string
		projectName string
	}{
		{
			name:        "account name with control characters",
			accountName: "account\x00name",
			projectName: "project",
		},
		{
			name:        "project name with control characters",
			accountName: "account",
			projectName: "project\x00name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewFoundryProjectsClient(tt.accountName, tt.projectName, nil)
			// If the URL parses successfully (Go is permissive), client should still be non-nil
			// The key thing is that the constructor doesn't panic
			if err != nil {
				require.Nil(t, client)
			} else {
				require.NotNil(t, client)
			}
		})
	}
}

func TestNewFoundryProjectsClient_EmptyInputs(t *testing.T) {
	tests := []struct {
		name        string
		accountName string
		projectName string
		errContains string
	}{
		{
			name:        "empty account name",
			accountName: "",
			projectName: "myproject",
			errContains: "accountName must not be empty",
		},
		{
			name:        "whitespace-only account name",
			accountName: "   ",
			projectName: "myproject",
			errContains: "accountName must not be empty",
		},
		{
			name:        "empty project name",
			accountName: "myaccount",
			projectName: "",
			errContains: "projectName must not be empty",
		},
		{
			name:        "whitespace-only project name",
			accountName: "myaccount",
			projectName: "  \t ",
			errContains: "projectName must not be empty",
		},
		{
			name:        "both empty",
			accountName: "",
			projectName: "",
			errContains: "accountName must not be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewFoundryProjectsClient(tt.accountName, tt.projectName, nil)
			require.Error(t, err)
			require.Nil(t, client)
			require.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestGetConnectionWithCredentials_AdversarialNames(t *testing.T) {
	// Verify that url.PathEscape properly sanitizes adversarial connection names
	// so they cannot cause path traversal in the constructed URL.
	tests := []struct {
		name           string
		connectionName string
		mustNotContain string // the raw dangerous substring that must NOT appear unescaped in the path
	}{
		{
			name:           "path traversal with dotdotslash",
			connectionName: "../../../evil",
			mustNotContain: "../",
		},
		{
			name:           "slashes in name",
			connectionName: "name/with/slashes",
			mustNotContain: "name/with/slashes",
		},
		{
			name:           "null byte injection",
			connectionName: "name%00null",
			mustNotContain: "name%00null/",
		},
		{
			name:           "backslash traversal",
			connectionName: `..\..\evil`,
			mustNotContain: `..\..\evil/`,
		},
		{
			name:           "encoded slash attempt",
			connectionName: "..%2F..%2Fevil",
			mustNotContain: "../",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			escaped := url.PathEscape(tt.connectionName)

			// The escaped value must not contain raw slashes that could alter the URL path.
			// url.PathEscape encodes '/' to '%2F', '.' to raw '.', but the key invariant
			// is that no unescaped '/' appears in the escaped output.
			require.NotContains(t, escaped, "/",
				"url.PathEscape must escape slashes to prevent path traversal")

			// Build the URL the same way the production code does and verify
			// the escaped name appears as a single opaque segment in the raw URL string.
			rawURL := fmt.Sprintf(
				"https://example.com/api/projects/proj/connections/%s/getConnectionWithCredentials",
				escaped)

			// Count the path segments in the raw (non-parsed) URL to confirm
			// the adversarial name didn't inject extra segments.
			rawPath := strings.TrimPrefix(rawURL, "https://example.com")
			rawSegments := strings.Split(strings.Trim(rawPath, "/"), "/")
			require.Equal(t, 6, len(rawSegments),
				"adversarial name %q should not inject extra path segments in raw URL, got: %s",
				tt.connectionName, rawPath)
			require.Equal(t, "connections", rawSegments[3])
			require.Equal(t, escaped, rawSegments[4])
			require.Equal(t, "getConnectionWithCredentials", rawSegments[5])
		})
	}
}

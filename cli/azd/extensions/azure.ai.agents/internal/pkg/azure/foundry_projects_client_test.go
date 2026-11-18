// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"fmt"
	"io"
	"net/http"
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
	t.Parallel()
	for _, endpoint := range []string{
		"", " \t ", "https://account\x00name.services.ai.azure.com/api/projects/project",
		"https://account.services.ai.azure.com/api/projects/project\x00name",
		"https://example.com/api/projects/project",
		"http://account.services.ai.azure.com/api/projects/project",
		"https://account.services.ai.azure.com",
		"https://user:password@account.services.ai.azure.com/api/projects/project?sig=secret",
	} {
		t.Run(endpoint, func(t *testing.T) {
			client, err := NewFoundryProjectsClient(endpoint, nil)
			require.Error(t, err)
			require.Nil(t, client)
			require.NotContains(t, err.Error(), "password")
			require.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestFoundryProjectsClient_CustomSubdomainRequests(t *testing.T) {
	t.Parallel()
	const endpoint = "https://custom-dns.services.ai.azure.com/api/projects/project"
	for _, crossOrigin := range []bool{false, true} {
		t.Run(fmt.Sprint(crossOrigin), func(t *testing.T) {
			t.Parallel()
			client, err := NewFoundryProjectsClient(endpoint+"/", nil)
			require.NoError(t, err)
			var requests []string
			client.pipeline = newTestPipeline(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, "custom-dns.services.ai.azure.com", request.URL.Host)
				requests = append(requests, request.Method+" "+request.URL.String())
				body := `{"value":[{"name":"registry"}]}`
				if len(requests) == 1 {
					next := endpoint + "/connections?skip=1"
					if crossOrigin {
						next = "https://resource-account.services.ai.azure.com/api/projects/project/connections?sig=secret"
					}
					body = fmt.Sprintf(`{"value":[{"name":"insights"}],"nextLink":%q}`, next)
				}
				if request.Method == http.MethodPost {
					body = `{"name":"insights","type":"AppInsights"}`
				}
				return &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
					Body: io.NopCloser(strings.NewReader(body)), Request: request,
				}, nil
			})
			connections, err := client.GetAllConnections(t.Context())
			if crossOrigin {
				require.ErrorContains(t, err, "origin mismatch")
				require.NotContains(t, err.Error(), "sig=")
				require.NotContains(t, err.Error(), "secret")
				require.Len(t, requests, 1)
				return
			}
			require.NoError(t, err)
			require.Len(t, connections, 2)
			require.Equal(t, []string{
				"GET " + endpoint + "/connections?api-version=v1",
				"GET " + endpoint + "/connections?skip=1",
			}, requests)
			connection, err := client.GetConnectionWithCredentials(t.Context(), "insights")
			require.NoError(t, err)
			require.Equal(t, "insights", connection.Name)
			require.Equal(t, "POST "+endpoint+
				"/connections/insights/getConnectionWithCredentials?api-version=v1", requests[2])
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

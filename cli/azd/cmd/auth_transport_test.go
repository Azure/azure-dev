// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBuildExternalAuthConfiguration_Schemes exercises the scheme dispatch in
// buildExternalAuthConfiguration. Per-scheme transport construction (unix
// permission checks, Windows pipe SD checks) is covered by the platform-
// specific tests in auth_transport_unix_test.go / auth_transport_windows_test.go.
func TestBuildExternalAuthConfiguration_Schemes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		endpoint    string
		key         string
		cert        string
		wantErrSub  string // substring expected in error message; empty means no error expected
		wantRewrite string // expected Endpoint on success; empty to skip
	}{
		{
			name:     "empty endpoint, no cert preserves backward compat",
			endpoint: "",
			key:      "k",
			cert:     "",
		},
		{
			name:     "https without cert keeps current behavior (no cert required at config time)",
			endpoint: "https://127.0.0.1:1234",
			key:      "k",
			cert:     "",
		},
		{
			name:       "http with cert is rejected because cert requires https",
			endpoint:   "http://127.0.0.1:1234",
			key:        "k",
			cert:       "not-a-real-cert",
			wantErrSub: "AZD_AUTH_CERT", // cert parse failure fires first
		},
		{
			name:     "http without cert is preserved for backward compat",
			endpoint: "http://127.0.0.1:1234",
			key:      "k",
			cert:     "",
		},
		{
			name:       "unix scheme rejects cert",
			endpoint:   "unix:/tmp/some.sock",
			key:        "k",
			cert:       "anything",
			wantErrSub: "AZD_AUTH_CERT must not be set",
		},
		{
			name:       "npipe scheme rejects cert",
			endpoint:   "npipe:azd-auth-x",
			key:        "k",
			cert:       "anything",
			wantErrSub: "AZD_AUTH_CERT must not be set",
		},
		{
			name:       "unknown scheme is refused with a list of supported schemes",
			endpoint:   "ftp://nope",
			key:        "k",
			cert:       "",
			wantErrSub: "supported schemes: https, unix, npipe",
		},
		{
			name:       "malformed url is reported",
			endpoint:   "://broken",
			wantErrSub: "invalid AZD_AUTH_ENDPOINT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := buildExternalAuthConfiguration(tt.endpoint, tt.key, tt.cert)
			if tt.wantErrSub != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErrSub)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, cfg.Transporter)
			require.Equal(t, tt.key, cfg.Key)
			if tt.wantRewrite != "" {
				require.Equal(t, tt.wantRewrite, cfg.Endpoint)
			} else {
				require.Equal(t, tt.endpoint, cfg.Endpoint)
			}
		})
	}
}

// TestRewrittenAuthEndpoint_FormatsValidURL verifies that the placeholder
// endpoint produces a syntactically valid request URL when concatenated by
// RemoteCredential with "/token?api-version=...".
func TestRewrittenAuthEndpoint_FormatsValidURL(t *testing.T) {
	t.Parallel()
	require.True(t, strings.HasPrefix(rewrittenAuthEndpoint, "http://"),
		"placeholder must be an absolute URL so net/http accepts it")
	require.NotContains(t, rewrittenAuthEndpoint, " ")
}

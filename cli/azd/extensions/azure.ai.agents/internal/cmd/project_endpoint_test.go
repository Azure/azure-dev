// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateProjectEndpoint_ValidURLs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		input       string
		want        string
		wantWarning bool
	}{
		{
			name:  "canonical URL",
			input: "https://my-acct.services.ai.azure.com/api/projects/my-proj",
			want:  "https://my-acct.services.ai.azure.com/api/projects/my-proj",
		},
		{
			name:  "trailing slash stripped",
			input: "https://my-acct.services.ai.azure.com/api/projects/my-proj/",
			want:  "https://my-acct.services.ai.azure.com/api/projects/my-proj",
		},
		{
			name:  "whitespace trimmed",
			input: "  https://my-acct.services.ai.azure.com/api/projects/my-proj  ",
			want:  "https://my-acct.services.ai.azure.com/api/projects/my-proj",
		},
		{
			name:  "uppercase host normalized",
			input: "https://MY-ACCT.SERVICES.AI.AZURE.COM/api/projects/my-proj",
			want:  "https://my-acct.services.ai.azure.com/api/projects/my-proj",
		},
		{
			name:        "missing /api/projects path warns",
			input:       "https://my-acct.services.ai.azure.com",
			want:        "https://my-acct.services.ai.azure.com",
			wantWarning: true,
		},
		{
			name:        "partial path warns",
			input:       "https://my-acct.services.ai.azure.com/api/projects/",
			want:        "https://my-acct.services.ai.azure.com/api/projects",
			wantWarning: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, warn, err := validateProjectEndpoint(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantWarning, warn)
		})
	}
}

func TestValidateProjectEndpoint_Rejections(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"http scheme", "http://my-acct.services.ai.azure.com/api/projects/p"},
		{"non-foundry host", "https://example.com/api/projects/p"},
		{"explicit port", "https://my-acct.services.ai.azure.com:8080/api/projects/p"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := validateProjectEndpoint(tt.input)
			require.Error(t, err)
			var localErr *azdext.LocalError
			assert.ErrorAs(t, err, &localErr)
		})
	}
}

func TestIsFoundryHost(t *testing.T) {
	t.Parallel()
	assert.True(t, isFoundryHost("my-acct.services.ai.azure.com"))
	assert.True(t, isFoundryHost("MY-ACCT.SERVICES.AI.AZURE.COM"))
	assert.False(t, isFoundryHost("example.com"))
	assert.False(t, isFoundryHost(""))
}

func TestNoProjectEndpointError(t *testing.T) {
	t.Parallel()
	err := noProjectEndpointError()
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, exterrors.CodeMissingProjectEndpoint, localErr.Code)
	assert.Equal(t, azdext.LocalErrorCategoryDependency, localErr.Category)
}

// TestValidateProjectEndpoint_OverrideBypass verifies that when the
// AZD_FOUNDRY_ENDPOINT_OVERRIDE env var is set, the validator accepts
// http:// URLs targeting localhost (or any host) with an explicit port. This
// is the developer-only path used to point the extension at a locally
// running Foundry backend such as the vienna managed-harness service.
//
// The test cannot use t.Parallel() because t.Setenv mutates process-global
// state; the validator reads the env var on every call.
func TestValidateProjectEndpoint_OverrideBypass(t *testing.T) {
	t.Setenv(FoundryEndpointOverrideEnvVar, "1")

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "http localhost with port",
			input: "http://localhost:5000",
			want:  "http://localhost:5000",
		},
		{
			name:  "http loopback ipv4",
			input: "http://127.0.0.1:5000",
			want:  "http://127.0.0.1:5000",
		},
		{
			name:  "https arbitrary host with port",
			input: "https://my-dev-box.internal:8443",
			want:  "https://my-dev-box.internal:8443",
		},
		{
			name:  "trailing slash stripped",
			input: "http://localhost:5000/",
			want:  "http://localhost:5000",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := validateProjectEndpoint(tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestValidateProjectEndpoint_OverrideOff verifies the bypass is opt-in:
// when the env var is empty the validator restores its strict checks.
func TestValidateProjectEndpoint_OverrideOff(t *testing.T) {
	t.Setenv(FoundryEndpointOverrideEnvVar, "")

	_, _, err := validateProjectEndpoint("http://localhost:5000")
	require.Error(t, err, "http://localhost should be rejected when override is off")

	_, _, err = validateProjectEndpoint("https://example.com/api/projects/p")
	require.Error(t, err, "non-foundry host should be rejected when override is off")
}

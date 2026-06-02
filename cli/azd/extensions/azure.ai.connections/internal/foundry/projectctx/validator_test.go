// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"testing"

	"azure.ai.connections/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate_ValidURLs(t *testing.T) {
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
			got, warn, err := Validate(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantWarning, warn)
		})
	}
}

func TestValidate_Rejections(t *testing.T) {
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
		{"malformed URL", "https://%zz/api/projects/p"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := Validate(tt.input)
			require.Error(t, err)
			var localErr *azdext.LocalError
			require.ErrorAs(t, err, &localErr)
			assert.Equal(t, exterrors.CodeInvalidParameter, localErr.Code)
		})
	}
}

func TestIsFoundryHost(t *testing.T) {
	t.Parallel()
	assert.True(t, isFoundryHost("my-acct.services.ai.azure.com"))
	assert.True(t, isFoundryHost("MY-ACCT.SERVICES.AI.AZURE.COM"))
	assert.False(t, isFoundryHost("example.com"))
	assert.False(t, isFoundryHost(""))
	assert.False(t, isFoundryHost("services.ai.azure.com.evil.com"))
}

func TestNoEndpointError(t *testing.T) {
	t.Parallel()
	err := NoEndpointError()
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, exterrors.CodeMissingProjectEndpoint, localErr.Code)
	assert.Equal(t, azdext.LocalErrorCategoryDependency, localErr.Category)
	assert.NotEmpty(t, localErr.Suggestion,
		"users need an actionable suggestion when no endpoint resolves")
}

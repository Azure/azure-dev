// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_RedactSensitiveData(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "redacts authorization header",
			input:    "Authorization: Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9",
			expected: "Authorization: [REDACTED]",
		},
		{
			name:     "redacts bearer token in text",
			input:    "got error with Bearer eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature",
			expected: "got error with Bearer [REDACTED]",
		},
		{
			name:     "redacts token query parameter",
			input:    "https://example.com/api?token=abc123def456&other=value",
			expected: "https://example.com/api?token=[REDACTED]&other=value",
		},
		{
			name:     "no redaction needed",
			input:    "normal log message without sensitive data",
			expected: "normal log message without sensitive data",
		},
		{
			name:     "multiple sensitive values",
			input:    "Authorization: secret123 and token=abc456 found",
			expected: "Authorization: [REDACTED]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := redactSensitiveData(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

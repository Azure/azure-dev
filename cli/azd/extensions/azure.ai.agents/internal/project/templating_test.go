// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpandEnv(t *testing.T) {
	env := map[string]string{
		"FOO":      "bar",
		"ENDPOINT": "https://example.com",
	}
	mapping := func(name string) string { return env[name] }

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "plain var", input: "${FOO}", want: "bar"},
		{name: "var with default present", input: "${FOO:-fallback}", want: "bar"},
		{name: "var with default absent", input: "${MISSING:-fallback}", want: "fallback"},
		{name: "missing var no default", input: "${MISSING}", want: ""},
		{name: "no templating", input: "plain text", want: "plain text"},
		{name: "empty string", input: "", want: ""},
		{name: "embedded var", input: "url=${ENDPOINT}/api", want: "url=https://example.com/api"},
		{
			name:  "foundry expression preserved",
			input: "${{connections.x.credentials.key}}",
			want:  "${{connections.x.credentials.key}}",
		},
		{
			name:  "mixed var and foundry expression",
			input: "prefix ${FOO} ${{event.body}} suffix",
			want:  "prefix bar ${{event.body}} suffix",
		},
		{
			name:  "adjacent foundry and var spans",
			input: "${{a}}${FOO}${{b}}",
			want:  "${{a}}bar${{b}}",
		},
		{
			name:  "multiple foundry expressions only",
			input: "${{a}} and ${{b}}",
			want:  "${{a}} and ${{b}}",
		},
		{
			name:  "foundry span across newline with var",
			input: "${{first\nsecond}}\n${FOO}",
			want:  "${{first\nsecond}}\nbar",
		},
		{
			name:    "malformed unterminated expression returns original",
			input:   "${{ unterminated",
			want:    "${{ unterminated",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandEnv(tt.input, mapping)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestExpandEnvNeverCorruptsFoundryExpressions guards the core invariant: a ${{...}} span is
// always returned byte-for-byte, regardless of what surrounds it.
func TestExpandEnvNeverCorruptsFoundryExpressions(t *testing.T) {
	mapping := func(name string) string { return map[string]string{"TOKEN": "secret"}[name] }

	const foundry = "${{connections.github-mcp-conn.credentials.x-api-key}}"
	got, err := ExpandEnv("Authorization: ${TOKEN}; key: "+foundry, mapping)
	require.NoError(t, err)
	assert.Equal(t, "Authorization: secret; key: "+foundry, got)
	assert.Contains(t, got, foundry)
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"slices"
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
			name:  "var inside foundry span not expanded",
			input: "${{ outer ${FOO} }}",
			want:  "${{ outer ${FOO} }}",
		},
		{
			name:  "foundry expression as default value, var unset",
			input: "${MISSING:-${{event.body}}}",
			want:  "${{event.body}}",
		},
		{
			name:  "foundry expression as default value, var set",
			input: "${FOO:-${{event.body}}}",
			want:  "bar",
		},
		{
			name:  "literal dollar before foundry span",
			input: "$${{event.body}}",
			want:  "$${{event.body}}",
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

func TestAnalyzeEnvReferences(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		want        []string
		hasTemplate bool
		wantErr     bool
	}{
		{name: "plain text"},
		{
			name:        "single required variable",
			input:       "${TOKEN}",
			want:        []string{"TOKEN"},
			hasTemplate: true,
		},
		{
			name:        "deduplicated and sorted",
			input:       "${ZED}-${ALPHA}-${ZED}",
			want:        []string{"ALPHA", "ZED"},
			hasTemplate: true,
		},
		{
			name:        "defaulted variable is optional",
			input:       "${OPTIONAL:-fallback}",
			hasTemplate: true,
		},
		{
			name:        "empty default is optional",
			input:       "${OPTIONAL:-}",
			hasTemplate: true,
		},
		{
			name:        "nested required reference in default",
			input:       "${OUTER:-${INNER}}",
			want:        []string{"INNER"},
			hasTemplate: true,
		},
		{
			name:        "nested defaulted references are optional",
			input:       "${OUTER:-${INNER:-fallback}}",
			hasTemplate: true,
		},
		{
			name:        "supported default operators",
			input:       "${A=default}${B:=default}${C:+default}${D:?message}",
			want:        []string{"D"},
			hasTemplate: true,
		},
		{
			name:        "value transformations are required",
			input:       "${NAME^^}-${#COUNT}",
			want:        []string{"COUNT", "NAME"},
			hasTemplate: true,
		},
		{
			name:  "escaped and server expressions are ignored",
			input: "$${ESCAPED} ${{connections.${SERVER}}}",
		},
		{
			name:        "mixed escaped server and client expressions",
			input:       "$${ESCAPED}-${{event.body}}-${LIVE}",
			want:        []string{"LIVE"},
			hasTemplate: true,
		},
		{
			name:    "malformed variable expression",
			input:   "private-value ${BROKEN",
			wantErr: true,
		},
		{
			name:    "unterminated Foundry expression",
			input:   "private-value ${{BROKEN",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AnalyzeEnvReferences(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				assert.NotContains(t, err.Error(), "private-value")
				assert.NotContains(t, err.Error(), "BROKEN")
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got.RequiredVariables)
			assert.Equal(t, tt.hasTemplate, got.HasClientTemplate)
		})
	}
}

func TestAnalyzeEnvReferencesMatchesExpandEnvParser(t *testing.T) {
	tests := []string{
		"plain value",
		"${A}",
		"$${A}",
		"${A:-fallback}",
		"${OUTER:-prefix-${INNER}}",
		"${VALUE^^}",
		"${VALUE/old/new}",
		"${{connections.name.credentials.key}}",
		"${VALUE:-${{event.body}}}",
		"${BROKEN",
		"${VALUE:-}",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			_, analysisErr := AnalyzeEnvReferences(input)
			_, expandErr := ExpandEnv(input, func(string) string { return "" })
			assert.Equal(t, expandErr == nil, analysisErr == nil)
		})
	}
}

func TestAnalyzeEnvReferencesUsesSharedFoundryMasking(t *testing.T) {
	value := "${{outer ${INNER}}} ${LIVE}"
	analysis, err := AnalyzeEnvReferences(value)
	require.NoError(t, err)
	assert.Equal(t, []string{"LIVE"}, analysis.RequiredVariables)
	assert.True(t, analysis.HasClientTemplate)

	expanded, err := ExpandEnv(value, func(name string) string {
		return "resolved-" + name
	})
	require.NoError(t, err)
	assert.Equal(t, "${{outer ${INNER}}} resolved-LIVE", expanded)
}

func TestAnalyzeEnvReferencesEmptyResultIsSortedSlice(t *testing.T) {
	analysis, err := AnalyzeEnvReferences("$${LITERAL} ${{server}}")
	require.NoError(t, err)
	assert.True(t, slices.Equal([]string{}, analysis.RequiredVariables))
	assert.False(t, analysis.HasClientTemplate)
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

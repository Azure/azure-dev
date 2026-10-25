// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"testing"

	corefoundry "github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeEnvReferences(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		want           []string
		wantReferenced []string
		hasTemplate    bool
		wantErr        bool
	}{
		{name: "plain text"},
		{
			name:           "required variable",
			input:          "${TOKEN}",
			want:           []string{"TOKEN"},
			wantReferenced: []string{"TOKEN"},
			hasTemplate:    true,
		},
		{
			name:           "sorted and deduplicated",
			input:          "${ZED}-${ALPHA}-${ZED}",
			want:           []string{"ALPHA", "ZED"},
			wantReferenced: []string{"ALPHA", "ZED"},
			hasTemplate:    true,
		},
		{
			name:           "defaulted reference is optional",
			input:          "${OPTIONAL:-fallback}",
			wantReferenced: []string{"OPTIONAL"},
			hasTemplate:    true,
		},
		{
			name:           "empty default is optional",
			input:          "${OPTIONAL:-}",
			wantReferenced: []string{"OPTIONAL"},
			hasTemplate:    true,
		},
		{
			name:           "nested required reference",
			input:          "${OUTER:-${INNER}}",
			want:           []string{"INNER"},
			wantReferenced: []string{"INNER", "OUTER"},
			hasTemplate:    true,
		},
		{
			name:           "nested defaults are optional",
			input:          "${OUTER:-${INNER:-fallback}}",
			wantReferenced: []string{"INNER", "OUTER"},
			hasTemplate:    true,
		},
		{
			name:           "supported default operators",
			input:          "${A=default}${B:=default}${C:+default}${D:?message}",
			wantReferenced: []string{"A", "B", "C", "D"},
			hasTemplate:    true,
		},
		{
			name:           "value transformations are required",
			input:          "${NAME^^}-${#COUNT}",
			want:           []string{"COUNT", "NAME"},
			wantReferenced: []string{"COUNT", "NAME"},
			hasTemplate:    true,
		},
		{
			name:  "escaped and Foundry references are ignored",
			input: "$${ESCAPED} ${{connections.${SERVER}}}",
		},
		{
			name:           "mixed Foundry and client references",
			input:          "$${ESCAPED}-${{event.body}}-${LIVE}",
			want:           []string{"LIVE"},
			wantReferenced: []string{"LIVE"},
			hasTemplate:    true,
		},
		{
			name:    "malformed client reference",
			input:   "private-marker ${BROKEN",
			wantErr: true,
		},
		{
			name:    "malformed Foundry expression",
			input:   "private-marker ${{BROKEN",
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := AnalyzeEnvReferences(test.input)
			if test.wantErr {
				require.Error(t, err)
				assert.NotContains(t, err.Error(), "private-marker")
				assert.NotContains(t, err.Error(), "BROKEN")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got.RequiredVariables)
			assert.Equal(t, test.wantReferenced, got.ReferencedVariables)
			assert.Equal(t, test.hasTemplate, got.HasClientTemplate)
		})
	}
}

func TestAnalyzeEnvReferencesReportsLiteralText(t *testing.T) {
	for input, want := range map[string]bool{
		"":                          false,
		"   ":                       false,
		"${A}":                      false,
		" ${A} ${B:-} ":             false,
		"plain":                     true,
		"https://example.test/${A}": true,
		"${A}-suffix":               true,
		"$${ESCAPED}":               true,
		"${{event.body}}":           true,
	} {
		t.Run(input, func(t *testing.T) {
			got, err := AnalyzeEnvReferences(input)
			require.NoError(t, err)
			assert.Equal(t, want, got.HasLiteralText)
		})
	}
}

func TestAnalyzeEnvReferencesMatchesSDKExpansionParser(t *testing.T) {
	for _, input := range []string{
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
	} {
		t.Run(input, func(t *testing.T) {
			_, analyzeErr := AnalyzeEnvReferences(input)
			_, expandErr := corefoundry.ExpandEnv(input, func(string) string { return "" })
			assert.Equal(t, expandErr == nil, analyzeErr == nil)
		})
	}
}

func TestAnalyzeEnvReferencesExactEmptyExpansion(t *testing.T) {
	for input, mayBeEmpty := range map[string]bool{
		"":                        true,
		" ":                       false,
		"${A}":                    true,
		" ${A} ":                  false,
		"${A:-}":                  true,
		"${A:- }":                 false,
		"${A:-fallback}":          false,
		"${A=fallback}":           false,
		"${A:=fallback}":          false,
		"${A:-${B}}":              true,
		"${A:-${B:-}}":            true,
		"${A:-${B:-fallback}}":    false,
		"${A:-prefix-${B}}":       false,
		"${A:+alternative}":       false,
		"${A:-${B:+alternative}}": false,
		"${A:?message}":           false,
		"${A:+}":                  true,
		"${A:?}":                  true,
		"${A:+${B:-}}":            true,
		"${A:?${B:-fallback}}":    false,
		"${A/old/new}":            true,
		"$${ESCAPED}":             false,
		"${{event.body}}":         false,
		"${A:-${{event.body}}}":   false,
	} {
		t.Run(input, func(t *testing.T) {
			got, err := AnalyzeEnvReferences(input)
			require.NoError(t, err)
			assert.Equal(t, mayBeEmpty, got.MayBeEmpty)
			if mayBeEmpty {
				return
			}
			for _, value := range []string{"", " ", "value"} {
				expanded, err := corefoundry.ExpandEnv(input, func(string) string { return value })
				require.NoError(t, err)
				assert.NotEmpty(t, expanded)
			}
		})
	}
}

func TestAnalyzeEnvReferencesSDKFallbackSemantics(t *testing.T) {
	for _, operator := range []string{":-", ":+", ":?"} {
		for _, fallback := range []string{"", "fallback", "${INNER:-}", "${INNER:-fallback}"} {
			input := "${VALUE" + operator + fallback + "}"
			t.Run(input, func(t *testing.T) {
				analysis, err := AnalyzeEnvReferences(input)
				require.NoError(t, err)
				assert.Empty(t, analysis.RequiredVariables)
				assert.Contains(t, analysis.ReferencedVariables, "VALUE")
				for _, values := range []map[string]string{
					{}, {"VALUE": ""}, {"VALUE": "value"}, {"VALUE": " "},
				} {
					actual, err := corefoundry.ExpandEnv(input, func(key string) string { return values[key] })
					require.NoError(t, err)
					expected, err := corefoundry.ExpandEnv(
						"${VALUE:-"+fallback+"}", func(key string) string { return values[key] },
					)
					require.NoError(t, err)
					assert.Equal(t, expected, actual)
					if !analysis.MayBeEmpty {
						assert.NotEmpty(t, actual)
					}
				}
			})
		}
	}
}

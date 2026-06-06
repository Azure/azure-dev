// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractUnresolvedPlaceholders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "empty template returns nil",
			input:    "",
			expected: nil,
		},
		{
			name:     "fully substituted template returns nil",
			input:    "key: real-value\nother: another-real-value\n",
			expected: nil,
		},
		{
			name:     "single placeholder",
			input:    "endpoint: {{TOOLBOX_ENDPOINT}}\n",
			expected: []string{"TOOLBOX_ENDPOINT"},
		},
		{
			name:     "multiple distinct placeholders sorted alphabetically",
			input:    "a: {{ZEBRA}}\nb: {{APPLE}}\nc: {{MANGO}}\n",
			expected: []string{"APPLE", "MANGO", "ZEBRA"},
		},
		{
			name:     "duplicate placeholders deduplicated",
			input:    "a: {{NAME}}-{{NAME}}\nb: {{NAME}}\n",
			expected: []string{"NAME"},
		},
		{
			name:     "hyphenated paramName captured",
			input:    "endpoint: {{toolbox-endpoint}}\n",
			expected: []string{"toolbox-endpoint"},
		},
		{
			name:     "dotted paramName captured",
			input:    "component: {{my.component.id}}\n",
			expected: []string{"my.component.id"},
		},
		{
			name:     "whitespace inside braces tolerated and stripped",
			input:    "a: {{ FOO }}\nb: {{  BAR  }}\n",
			expected: []string{"BAR", "FOO"},
		},
		{
			name:     "empty braces do not match",
			input:    "a: {{}}\nb: real-value\n",
			expected: nil,
		},
		{
			name:     "whitespace-only braces do not match",
			input:    "a: {{   }}\nb: real-value\n",
			expected: nil,
		},
		{
			name:     "spaced and unspaced forms of the same name deduplicated",
			input:    "a: {{NAME}}\nb: {{ NAME }}\n",
			expected: []string{"NAME"},
		},
		{
			name:     "mixed real values and placeholders",
			input:    "a: actual\nb: {{MISSING_ONE}}\nc: actual\nd: {{MISSING_TWO}}\n",
			expected: []string{"MISSING_ONE", "MISSING_TWO"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ExtractUnresolvedPlaceholders(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

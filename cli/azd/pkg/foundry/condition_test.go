// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluateCondition(t *testing.T) {
	env := map[string]string{
		"ENABLE_CONNECTION":  "true",
		"DISABLE_CONNECTION": "false",
		"ENABLE_YES":         "YES",
	}
	lookup := func(name string) string { return env[name] }

	tests := []struct {
		name    string
		value   any
		getenv  func(string) string
		want    bool
		wantErr string
	}{
		{name: "nil is enabled", value: nil, want: true},
		{name: "empty string is enabled", value: "", want: true},
		{name: "whitespace is disabled", value: "  ", want: false},
		{name: "bool true", value: true, want: true},
		{name: "bool false", value: false, want: false},
		{name: "literal 1", value: "1", want: true},
		{name: "literal true", value: "true", want: true},
		{name: "literal TRUE", value: "TRUE", want: true},
		{name: "literal True", value: "True", want: true},
		{name: "literal yes", value: "yes", want: true},
		{name: "literal YES", value: "YES", want: true},
		{name: "literal Yes", value: "Yes", want: true},
		{name: "literal false", value: "false", want: false},
		{name: "literal 0", value: "0", want: false},
		{name: "int 1", value: 1, want: true},
		{name: "int 0", value: 0, want: false},
		{name: "float 1", value: 1.0, want: true},
		{
			name:   "expanded true",
			value:  "${ENABLE_CONNECTION}",
			getenv: lookup,
			want:   true,
		},
		{
			name:   "expanded false",
			value:  "${DISABLE_CONNECTION}",
			getenv: lookup,
			want:   false,
		},
		{
			name:   "expanded YES",
			value:  "${ENABLE_YES}",
			getenv: lookup,
			want:   true,
		},
		{
			name:   "missing var is false",
			value:  "${MISSING}",
			getenv: lookup,
			want:   false,
		},
		{
			name:    "map is invalid",
			value:   map[string]any{"x": true},
			wantErr: "condition must be a string, boolean, or number",
		},
		{
			name:    "list is invalid",
			value:   []any{"true"},
			wantErr: "condition must be a string, boolean, or number",
		},
		{
			name:    "malformed template",
			value:   "${",
			getenv:  lookup,
			wantErr: "malformed condition template",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EvaluateCondition(tt.value, tt.getenv)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

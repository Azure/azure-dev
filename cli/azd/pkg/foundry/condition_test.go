// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluateCondition(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		env     map[string]string
		want    bool
		wantErr string
	}{
		{
			name: "empty condition is enabled",
			want: true,
		},
		{
			name:  "literal 1 is enabled",
			value: "1",
			want:  true,
		},
		{
			name:  "literal true is enabled",
			value: "true",
			want:  true,
		},
		{
			name:  "literal TRUE is enabled",
			value: "TRUE",
			want:  true,
		},
		{
			name:  "literal True is enabled",
			value: "True",
			want:  true,
		},
		{
			name:  "literal yes is enabled",
			value: "yes",
			want:  true,
		},
		{
			name:  "literal YES is enabled",
			value: "YES",
			want:  true,
		},
		{
			name:  "literal Yes is enabled",
			value: "Yes",
			want:  true,
		},
		{
			name:  "truthy environment value enables service",
			value: "${ENABLE_CONNECTION}",
			env:   map[string]string{"ENABLE_CONNECTION": "true"},
			want:  true,
		},
		{
			name:  "default environment value enables service",
			value: "${ENABLE_CONNECTION:-yes}",
			want:  true,
		},
		{
			name:  "falsy environment value disables service",
			value: "${ENABLE_CONNECTION}",
			env:   map[string]string{"ENABLE_CONNECTION": "false"},
			want:  false,
		},
		{
			name:  "zero disables service",
			value: "0",
			want:  false,
		},
		{
			name:  "whitespace around truthy value disables service",
			value: " true ",
			want:  false,
		},
		{
			name:  "whitespace condition is disabled",
			value: " ",
			want:  false,
		},
		{
			name:  "missing environment value disables service",
			value: "${MISSING}",
			want:  false,
		},
		{
			name:  "empty environment value disables service",
			value: "${EMPTY}",
			env:   map[string]string{"EMPTY": ""},
			want:  false,
		},
		{
			name:    "Foundry expression is malformed",
			value:   "${{connections.foo}}",
			wantErr: "malformed condition template",
		},
		{
			name:    "unclosed environment expression is malformed",
			value:   "${UNCLOSED",
			wantErr: "malformed condition template",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EvaluateCondition(tt.value, func(name string) string {
				return tt.env[name]
			})
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEvaluateConditionWithNilEnvironmentLookup(t *testing.T) {
	got, err := EvaluateCondition("${MISSING}", nil)

	require.NoError(t, err)
	assert.False(t, got)
}

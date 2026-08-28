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
			name:  "truthy environment value enables service",
			value: "${ENABLE_CONNECTION}",
			env:   map[string]string{"ENABLE_CONNECTION": "true"},
			want:  true,
		},
		{
			name:  "falsy environment value disables service",
			value: "${ENABLE_CONNECTION}",
			env:   map[string]string{"ENABLE_CONNECTION": "false"},
			want:  false,
		},
		{
			name:  "whitespace condition is disabled",
			value: " ",
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

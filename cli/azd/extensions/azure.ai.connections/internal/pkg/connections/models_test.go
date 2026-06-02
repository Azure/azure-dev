// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package connections

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCredentials(t *testing.T) {
	tests := []struct {
		name           string
		raw            map[string]any
		wantType       string
		wantKey        string
		wantCustomKeys map[string]string
		wantNil        bool
	}{
		{
			name:    "nil input",
			raw:     nil,
			wantNil: true,
		},
		{
			name: "ApiKey credentials",
			raw: map[string]any{
				"type": "ApiKey",
				"key":  "my-secret-key",
			},
			wantType:       "ApiKey",
			wantKey:        "my-secret-key",
			wantCustomKeys: map[string]string{},
		},
		{
			name: "CustomKeys credentials",
			raw: map[string]any{
				"type":      "CustomKeys",
				"x-api-key": "tavily-key",
				"token":     "bearer-token",
			},
			wantType: "CustomKeys",
			wantKey:  "",
			wantCustomKeys: map[string]string{
				"x-api-key": "tavily-key",
				"token":     "bearer-token",
			},
		},
		{
			name: "AAD credentials (no secrets)",
			raw: map[string]any{
				"type": "AAD",
			},
			wantType:       "AAD",
			wantKey:        "",
			wantCustomKeys: map[string]string{},
		},
		{
			name: "mixed key and custom keys",
			raw: map[string]any{
				"type":  "ApiKey",
				"key":   "primary",
				"extra": "bonus",
			},
			wantType: "ApiKey",
			wantKey:  "primary",
			wantCustomKeys: map[string]string{
				"extra": "bonus",
			},
		},
		{
			name: "non-string values skipped",
			raw: map[string]any{
				"type":    "Custom",
				"key":     "valid",
				"numeric": 42,
				"nested":  map[string]any{"a": "b"},
			},
			wantType:       "Custom",
			wantKey:        "valid",
			wantCustomKeys: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseCredentials(tt.raw)
			if tt.wantNil {
				require.Nil(t, result)
				return
			}
			require.NotNil(t, result)
			require.Equal(t, tt.wantType, result.Type)
			require.Equal(t, tt.wantKey, result.Key)
			require.Equal(t, tt.wantCustomKeys, result.CustomKeys)

			// Verify RawFields contains non-type string fields
			for k, v := range tt.raw {
				if k == "type" {
					continue
				}
				if strVal, ok := v.(string); ok {
					require.Equal(t, strVal, result.RawFields[k],
						"RawFields[%q] mismatch", k)
				}
			}
		})
	}
}

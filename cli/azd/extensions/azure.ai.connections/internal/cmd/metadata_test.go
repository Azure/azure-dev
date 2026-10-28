// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarkNoAzdEnvironmentCommand(t *testing.T) {
	tests := []struct {
		name   string
		target string
		launch map[string]any
	}{
		{
			name:   "no launch object",
			target: `{"name": ["add"]}`,
			launch: map[string]any{"noAzdEnvironment": true},
		},
		{
			name:   "existing launch keys",
			target: `{"name": ["add"], "launch": {"other": {"enabled": true}, "noAzdEnvironment": false}}`,
			launch: map[string]any{"other": map[string]any{"enabled": true}, "noAzdEnvironment": true},
		},
		{
			name:   "null launch object",
			target: `{"name": ["add"], "launch": null}`,
			launch: map[string]any{"noAzdEnvironment": true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := []byte(`{"commands": [` + tt.target + `, {"name": ["remove"]}]}`)

			result, err := markNoAzdEnvironmentCommand(input, []string{"add"})
			require.NoError(t, err)

			var metadata struct {
				Commands []struct {
					Name   []string       `json:"name"`
					Launch map[string]any `json:"launch"`
				} `json:"commands"`
			}
			require.NoError(t, json.Unmarshal(result, &metadata))
			require.Len(t, metadata.Commands, 2)
			assert.Equal(t, []string{"add"}, metadata.Commands[0].Name)
			assert.Equal(t, tt.launch, metadata.Commands[0].Launch)
			assert.Equal(t, []string{"remove"}, metadata.Commands[1].Name)
			assert.Nil(t, metadata.Commands[1].Launch)
			assert.NotContains(t, string(result), `"environmentIndependent"`)
		})
	}
}

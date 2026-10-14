// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarkEnvironmentIndependentCommand(t *testing.T) {
	input := []byte(`{
		"commands": [
			{"name": ["add"]},
			{"name": ["remove"]}
		]
	}`)

	result, err := markEnvironmentIndependentCommand(input, []string{"add"})
	require.NoError(t, err)

	var metadata struct {
		Commands []struct {
			Name                   []string `json:"name"`
			EnvironmentIndependent bool     `json:"environmentIndependent"`
		} `json:"commands"`
	}
	require.NoError(t, json.Unmarshal(result, &metadata))
	require.Len(t, metadata.Commands, 2)
	assert.True(t, metadata.Commands[0].EnvironmentIndependent)
	assert.False(t, metadata.Commands[1].EnvironmentIndependent)
}

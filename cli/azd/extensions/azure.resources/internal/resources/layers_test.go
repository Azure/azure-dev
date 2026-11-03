// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLayerIdentityAndRepeatability(t *testing.T) {
	generator := &Generator{}
	for _, name := range []string{"", "catalog", "orders"} {
		t.Run(name, func(t *testing.T) {
			request := GenerateRequest{Name: name, Config: map[string]any{
				"cache": map[string]any{"type": Redis},
			}}
			files, err := generator.Generate(t.Context(), request)
			require.NoError(t, err)
			repeated, err := generator.Generate(t.Context(), request)
			require.NoError(t, err)
			require.Equal(t, files, repeated)
			var parameters struct {
				Parameters map[string]struct {
					Value string `json:"value"`
				} `json:"parameters"`
			}
			require.NoError(t, json.Unmarshal(files["main.parameters.json"], &parameters))
			if name == "" {
				require.NotContains(t, parameters.Parameters, "layerName")
				require.NotContains(t, string(files["resources.bicep"]), "layerName")
				require.Contains(t, string(files["resources.bicep"]),
					"uniqueString(subscription().id, resourceGroup().id, location)")
			} else {
				require.Equal(t, name, parameters.Parameters["layerName"].Value)
				require.Contains(t, string(files["main.bicep"]), "layerName: layerName")
				require.Contains(t, string(files["resources.bicep"]),
					"uniqueString(subscription().id, resourceGroup().id, location, layerName)")
			}
		})
	}
}

func TestEntryContentsAreIsolated(t *testing.T) {
	generator := &Generator{}
	for _, name := range []string{"catalog-items", "order-receipts"} {
		files, err := generator.Generate(t.Context(), GenerateRequest{Name: name, Config: map[string]any{
			"storage": map[string]any{"type": Storage, "containers": []any{name}},
		}})
		require.NoError(t, err)
		require.Contains(t, string(files["resources.bicep"]), "'"+name+"'")
		other := "catalog-items"
		if name == other {
			other = "order-receipts"
		}
		require.NotContains(t, string(files["resources.bicep"]), "'"+other+"'")
	}
}

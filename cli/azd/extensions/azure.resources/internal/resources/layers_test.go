// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateProjectLayers(t *testing.T) {
	for _, test := range []struct {
		name      string
		providers []string
		inherited string
		selected  string
		wantError bool
	}{
		{name: "single", providers: []string{"resources"}},
		{name: "mixed providers", providers: []string{"resources", "bicep"}},
		{name: "updating existing", providers: []string{"resources"}, selected: "layer0"},
		{name: "adding second", providers: []string{"resources"}, selected: "new", wantError: true},
		{name: "replacing another", providers: []string{"resources", "bicep"}, selected: "layer1", wantError: true},
		{name: "two resources", providers: []string{"resources", "resources"}, wantError: true},
		{name: "inherited provider", providers: []string{"", ""}, inherited: "resources", wantError: true},
		{name: "explicit override", providers: []string{"", "bicep"}, inherited: "resources"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var layers []any
			for i, provider := range test.providers {
				layer := map[string]any{"name": fmt.Sprintf("layer%d", i)}
				if provider != "" {
					layer["provider"] = provider
				}
				layers = append(layers, layer)
			}
			err := ValidateProjectLayers(map[string]any{
				"infra": map[string]any{"provider": test.inherited, "layers": layers},
			}, test.selected)
			if test.wantError {
				require.ErrorContains(t, err, "only one resources infrastructure entry")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateProjectLayersRejectsRepeatedTopLevelProviders(t *testing.T) {
	project := map[string]any{"layers": []any{
		map[string]any{"name": "data", "infra": []any{map[string]any{"name": "storage", "provider": ProviderName}}},
		map[string]any{"name": "app", "infra": []any{map[string]any{"name": "host", "provider": ProviderName}}},
	}}
	require.ErrorContains(t, ValidateProjectLayers(project, ""), "only one resources infrastructure entry")
}

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

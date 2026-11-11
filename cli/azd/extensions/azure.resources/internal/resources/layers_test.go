// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateProjectLayersRejectsInvalidProviders(t *testing.T) {
	// An explicit provider must be a string; null is not equivalent to an absent key.
	for _, test := range []struct {
		name     string
		provider any
	}{
		{name: "non-string explicit provider", provider: true},
		{name: "null explicit provider"},
	} {
		t.Run(test.name, func(t *testing.T) {
			layer := map[string]any{"name": "data", "provider": test.provider}
			infra := map[string]any{"layers": []any{layer}}
			err := ValidateProjectLayers(map[string]any{"infra": infra}, "")
			// Locate the invalid value by both its layer index and its authored name.
			require.ErrorContains(t, err, `infra.layers[0] layer "data" provider: "provider" must be string`)
		})
	}
}

func TestValidateProjectLayersRejectsInvalidFlatProvider(t *testing.T) {
	// Validate the flat infra form too, even when there is no layers list to traverse.
	err := ValidateProjectLayers(map[string]any{"infra": map[string]any{"provider": true}}, "")
	require.ErrorContains(t, err, `infra "provider" must be string`)
}

func TestValidateProjectLayersRejectsEmptyLayerNames(t *testing.T) {
	for _, test := range []struct {
		name  string
		layer map[string]any
	}{
		{name: "missing", layer: map[string]any{"provider": "bicep"}},
		{name: "empty", layer: map[string]any{"name": "", "provider": "bicep"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := map[string]any{"infra": map[string]any{"layers": []any{test.layer}}}
			require.EqualError(t, ValidateProjectLayers(project, ""),
				"infra.layers[0] requires a unique, nonempty name")
		})
	}
}

func TestValidateProjectLayersRejectsDuplicateLayerNames(t *testing.T) {
	project := map[string]any{"infra": map[string]any{"layers": []any{
		map[string]any{"name": "data", "provider": "bicep"},
		map[string]any{"name": "data", "provider": "bicep"},
	}}}

	for _, selected := range []string{"", "data"} {
		t.Run("selected="+selected, func(t *testing.T) {
			require.EqualError(t, ValidateProjectLayers(project, selected),
				"infra.layers[1] requires a unique, nonempty name")
		})
	}
}

func TestValidateProjectLayers(t *testing.T) {
	// selected names a layer that will use resources: updating the existing one is allowed,
	// but adding or converting another layer must not produce a second resources entry.
	for _, test := range []struct {
		name      string
		providers []string
		selected  string
		wantError bool
	}{
		{name: "single", providers: []string{"resources"}},
		{name: "mixed providers", providers: []string{"resources", "bicep"}},
		{name: "updating existing", providers: []string{"resources"}, selected: "layer0"},
		{name: "adding second", providers: []string{"resources"}, selected: "new", wantError: true},
		{name: "replacing another", providers: []string{"resources", "bicep"}, selected: "layer1", wantError: true},
		{name: "two resources", providers: []string{"resources", "resources"}, wantError: true},
		{name: "unspecified provider", providers: []string{"", "bicep"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var layers []any
			for i, provider := range test.providers {
				layer := map[string]any{"name": fmt.Sprintf("layer%d", i)}
				// An empty fixture string means the provider key is absent, not explicitly empty.
				if provider != "" {
					layer["provider"] = provider
				}
				layers = append(layers, layer)
			}
			err := ValidateProjectLayers(map[string]any{
				"infra": map[string]any{"layers": layers},
			}, test.selected)
			if test.wantError {
				require.ErrorContains(t, err, "only one resources infrastructure entry")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateProjectLayersDoesNotInheritProvider(t *testing.T) {
	// Only the parent infra declares resources; neither child layer declares a provider.
	project := map[string]any{
		"infra": map[string]any{
			"provider": ProviderName,
			"layers": []any{
				map[string]any{"name": "data"},
				map[string]any{"name": "app"},
			},
		}}

	// The children must not be counted as two resources entries.
	require.NoError(t, ValidateProjectLayers(project, ""))

	// They must not prevent selecting a new resources layer either.
	require.NoError(t, ValidateProjectLayers(project, "new"))
}

func TestValidateProjectLayersDoesNotInheritNestedProvider(t *testing.T) {
	// Shape: project layer data -> infra entry using resources -> storage/cache child layers.
	// Neither child explicitly declares resources, so they must not inflate the entry count.
	project := map[string]any{"layers": []any{
		map[string]any{
			"name": "data",
			"infra": []any{map[string]any{
				"provider": ProviderName,
				"layers": []any{
					map[string]any{"name": "storage"},
					map[string]any{"name": "cache"},
				},
			}}},
	}}

	require.NoError(t, ValidateProjectLayers(project, ""))
}

func TestValidateProjectLayersRejectsRepeatedTopLevelProviders(t *testing.T) {
	project := map[string]any{"layers": []any{
		map[string]any{"name": "data", "infra": []any{map[string]any{"name": "storage", "provider": ProviderName}}},
		map[string]any{"name": "app", "infra": []any{map[string]any{"name": "host", "provider": ProviderName}}},
	}}
	require.ErrorContains(t, ValidateProjectLayers(project, ""), "only one resources infrastructure entry")
}

func TestLayerIdentityAndRepeatability(t *testing.T) {
	for _, name := range []string{"", "catalog", "orders"} {
		t.Run(name, func(t *testing.T) {
			request := GenerateRequest{Name: name, Config: map[string]any{
				"cache": map[string]any{"type": Redis},
			}}
			files, err := GenerateFromConfig(t.Context(), request)
			require.NoError(t, err)
			repeated, err := GenerateFromConfig(t.Context(), request)
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
	for _, name := range []string{"catalog-items", "order-receipts"} {
		files, err := GenerateFromConfig(t.Context(), GenerateRequest{Name: name, Config: map[string]any{
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

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
)

func TestProjectLayerInfraSchemaRequiresProvider(t *testing.T) {
	schemaPath := filepath.Join("..", "..", "..", "..", "schemas", "alpha", "azure.yaml.json")
	raw, err := os.ReadFile(schemaPath)
	require.NoError(t, err)

	var schemaDocument map[string]any
	require.NoError(t, json.Unmarshal(raw, &schemaDocument))

	properties, ok := schemaDocument["properties"].(map[string]any)
	require.True(t, ok)
	infraSchema, ok := properties["infra"].(map[string]any)
	require.True(t, ok)
	infraProperties, ok := infraSchema["properties"].(map[string]any)
	require.True(t, ok)
	legacyLayers, ok := infraProperties["layers"].(map[string]any)
	require.True(t, ok)
	legacyItem, ok := legacyLayers["items"].(map[string]any)
	require.True(t, ok)

	// Remove unrelated local references so this focused subschema can compile independently.
	legacyItem = maps.Clone(legacyItem)
	legacyProperties, ok := legacyItem["properties"].(map[string]any)
	require.True(t, ok)
	legacyProperties = maps.Clone(legacyProperties)
	delete(legacyProperties, "deploymentStacks")
	delete(legacyProperties, "hooks")
	legacyItem["properties"] = legacyProperties

	projectLayers, ok := properties["layers"].(map[string]any)
	require.True(t, ok)
	projectLayerItem, ok := projectLayers["items"].(map[string]any)
	require.True(t, ok)
	projectLayerProperties, ok := projectLayerItem["properties"].(map[string]any)
	require.True(t, ok)
	projectInfra, ok := projectLayerProperties["infra"].(map[string]any)
	require.True(t, ok)
	projectInfraItem, ok := projectInfra["items"].(map[string]any)
	require.True(t, ok)
	allOf, ok := projectInfraItem["allOf"].([]any)
	require.True(t, ok)
	require.Len(t, allOf, 2)

	projectInfraItem = maps.Clone(projectInfraItem)
	projectAllOf := slices.Clone(allOf)
	projectAllOf[0] = legacyItem
	projectInfraItem["allOf"] = projectAllOf

	compile := func(t *testing.T, name string, document map[string]any) *jsonschema.Schema {
		t.Helper()
		compiler := jsonschema.NewCompiler()
		uri := "mem://" + name + ".json"
		require.NoError(t, compiler.AddResource(uri, document))
		compiled, err := compiler.Compile(uri)
		require.NoError(t, err)
		return compiled
	}

	projectSchema := compile(t, "project-layer-infra", projectInfraItem)
	require.Error(t, projectSchema.Validate(map[string]any{"name": "app", "path": "infra/app"}))
	require.NoError(t, projectSchema.Validate(map[string]any{
		"name": "app", "path": "infra/app", "provider": "bicep",
	}))

	legacySchema := compile(t, "legacy-infra-layer", legacyItem)
	require.NoError(t, legacySchema.Validate(map[string]any{"name": "backend", "path": "infra/backend"}))
}

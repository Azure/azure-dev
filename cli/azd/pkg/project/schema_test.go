// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"os"
	"path/filepath"
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

	definitions, ok := schemaDocument["definitions"].(map[string]any)
	require.True(t, ok)
	properties, ok := schemaDocument["properties"].(map[string]any)
	require.True(t, ok)
	resource := map[string]any{
		"definitions": map[string]any{
			"layerInfrastructure":    definitions["layerInfrastructure"],
			"deploymentStacksConfig": definitions["deploymentStacksConfig"],
			"hooks":                  definitions["hooks"],
			"hook":                   definitions["hook"],
			"jsHookConfig":           definitions["jsHookConfig"],
			"pythonHookConfig":       definitions["pythonHookConfig"],
			"dotnetHookConfig":       definitions["dotnetHookConfig"],
			"emptyHookConfig":        definitions["emptyHookConfig"],
		},
		"properties": map[string]any{"infra": properties["infra"]},
	}
	compiler := jsonschema.NewCompiler()
	const uri = "mem://azure.yaml.json"
	require.NoError(t, compiler.AddResource(uri, resource))
	compile := func(t *testing.T, fragment string) *jsonschema.Schema {
		t.Helper()
		compiled, err := compiler.Compile(uri + fragment)
		require.NoError(t, err)
		return compiled
	}

	projectSchema := compile(t, "#/definitions/layerInfrastructure")
	require.Error(t, projectSchema.Validate(map[string]any{"name": "app", "path": "infra/app"}))
	require.NoError(t, projectSchema.Validate(map[string]any{
		"name": "app", "provider": "microsoft.foundry",
	}))
	for _, provider := range []string{"bicep", "terraform"} {
		require.Error(t, projectSchema.Validate(map[string]any{"name": "app", "provider": provider}))
		require.NoError(t, projectSchema.Validate(map[string]any{
			"name": "app", "provider": provider, "path": "infra/app",
		}))
	}

	legacySchema := compile(t, "#/properties/infra/properties/layers/items")
	require.NoError(t, legacySchema.Validate(map[string]any{"name": "backend", "path": "infra/backend"}))
}

func TestLayerSchemaAlphaAllowsPathlessEntries(t *testing.T) {
	loadInfraProperties := func(t *testing.T, version string) map[string]any {
		t.Helper()
		schemaPath := filepath.Join("..", "..", "..", "..", "schemas", version, "azure.yaml.json")
		raw, err := os.ReadFile(schemaPath)
		require.NoError(t, err)

		var document map[string]any
		require.NoError(t, json.Unmarshal(raw, &document))
		properties, ok := document["properties"].(map[string]any)
		require.True(t, ok)
		infra, ok := properties["infra"].(map[string]any)
		require.True(t, ok)
		infraProperties, ok := infra["properties"].(map[string]any)
		require.True(t, ok)
		return infraProperties
	}

	alphaInfra := loadInfraProperties(t, "alpha")
	stableInfra := loadInfraProperties(t, "v1.0")

	alphaLayers, ok := alphaInfra["layers"].(map[string]any)
	require.True(t, ok)
	alphaLayer, ok := alphaLayers["items"].(map[string]any)
	require.True(t, ok)
	stableLayers, ok := stableInfra["layers"].(map[string]any)
	require.True(t, ok)
	stableLayer, ok := stableLayers["items"].(map[string]any)
	require.True(t, ok)
	require.ElementsMatch(t, []any{"name"}, alphaLayer["required"])
	require.ElementsMatch(t, []any{"name", "path"}, stableLayer["required"])
}

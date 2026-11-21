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

func TestResourceEnvSchema(t *testing.T) {
	t.Parallel()

	resources := []struct {
		name        string
		title       string
		description string
	}{
		{"appServiceResource", "Environment variables to set for the web app",
			"Optional. Environment variables to set for the App Service web app."},
		{"functionAppResource", "Additional Function App settings",
			"Optional. Additional application settings for the Function App."},
		{"containerAppResource", "Environment variables to set for the container app",
			"Optional. Environment variables to set for the container app."},
	}

	for _, version := range []string{"v1.0", "alpha"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()

			raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "schemas", version, "azure.yaml.json"))
			require.NoError(t, err)
			var document map[string]any
			require.NoError(t, json.Unmarshal(raw, &document))

			require.IsType(t, map[string]any{}, document["definitions"])
			definitions := document["definitions"].(map[string]any)
			require.IsType(t, map[string]any{}, definitions["serviceEnv"])
			envDefinition := definitions["serviceEnv"].(map[string]any)
			require.IsType(t, map[string]any{}, envDefinition["items"])
			items := envDefinition["items"].(map[string]any)
			require.IsType(t, map[string]any{}, items["properties"])
			itemProperties := items["properties"].(map[string]any)
			for _, key := range []string{"name", "value", "secret"} {
				require.IsType(t, map[string]any{}, itemProperties[key])
				require.NotEmpty(t, itemProperties[key].(map[string]any)["title"])
			}

			for _, resource := range resources {
				t.Run(resource.name, func(t *testing.T) {
					require.IsType(t, map[string]any{}, definitions[resource.name])
					properties := definitions[resource.name].(map[string]any)["properties"]
					require.IsType(t, map[string]any{}, properties)
					envProperty := properties.(map[string]any)["env"]
					require.IsType(t, map[string]any{}, envProperty)
					env := envProperty.(map[string]any)
					require.Equal(t, "#/definitions/serviceEnv", env["$ref"])
					require.Equal(t, resource.title, env["title"])
					require.Equal(t, resource.description, env["description"])

					const uri = "mem://resource-env.json"
					compiler := jsonschema.NewCompiler()
					require.NoError(t, compiler.AddResource(uri, map[string]any{
						"$schema": document["$schema"],
						"definitions": map[string]any{
							"serviceEnv": envDefinition,
							"env":        env,
						},
					}))
					schema, err := compiler.Compile(uri + "#/definitions/env")
					require.NoError(t, err)
					require.NoError(t, schema.Validate([]any{
						map[string]any{"name": "SETTING", "value": "value"},
						map[string]any{"name": "SECRET", "secret": "secret"},
					}))
					for _, invalid := range []any{
						[]any{map[string]any{"name": "SETTING"}},
						[]any{map[string]any{"name": "SETTING", "value": "v", "secret": "s"}},
						[]any{map[string]any{"value": "missing name"}},
						[]any{map[string]any{"name": "SETTING", "extra": "unknown"}},
					} {
						require.Error(t, schema.Validate(invalid))
					}
				})
			}
		})
	}
}

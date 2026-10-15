// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"azureaieval/internal/project"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExplicitLocalSourceSchemaMatchesRuntime(t *testing.T) {
	compiler := jsonschema.NewCompiler()
	const uri = "https://example.test/local.schema.json"
	require.NoError(t, compiler.AddResource(uri, evalSchemaDocument(t)))
	schema, err := compiler.Compile(uri)
	require.NoError(t, err)
	for _, tc := range []struct {
		name  string
		edit  func(map[string]any, map[string]any)
		valid bool
	}{
		{name: "local uncapped", valid: true},
		{name: "local capped", edit: func(eval, _ map[string]any) { eval["maxSamples"] = 3 }, valid: true},
		{name: "missing file", edit: func(_, source map[string]any) { delete(source, "file") }},
		{name: "blank file", edit: func(_, source map[string]any) { source["file"] = " " }},
		{name: "URL", edit: func(_, source map[string]any) { source["file"] = "https://example.test/rows.jsonl" }},
		{name: "mixed dataset", edit: func(eval, _ map[string]any) { eval["dataset"] = "golden" }},
		{name: "empty mixed dataset", edit: func(eval, _ map[string]any) { eval["dataset"] = "" }},
		{name: "negative cap", edit: func(eval, _ map[string]any) { eval["maxSamples"] = -1 }},
		{name: "simulation", edit: func(eval, _ map[string]any) {
			eval["simulation"] = map[string]any{"model": "connection/model"}
		}},
		{name: "trace field zero", edit: func(_, source map[string]any) { source["maxTraces"] = 0 }},
		{name: "response field empty", edit: func(_, source map[string]any) { source["responseIds"] = []string{} }},
		{name: "file on traces", edit: func(_, source map[string]any) {
			source["type"], source["agentName"] = "traces", "agent"
		}},
		{name: "file on responses", edit: func(_, source map[string]any) {
			source["type"], source["responseIds"] = "responses", []string{"one"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := map[string]any{"type": "local", "file": "rows.jsonl"}
			eval := map[string]any{
				"name": "quality", "source": source,
				"evaluators": []any{map[string]any{"evaluator": "builtin.relevance"}},
			}
			if tc.edit != nil {
				tc.edit(eval, source)
			}
			body, err := json.Marshal(map[string]any{"evals": []any{eval}})
			require.NoError(t, err)
			var instance any
			require.NoError(t, json.Unmarshal(body, &instance))
			schemaErr := schema.Validate(instance)
			cfg, runtimeErr := project.DecodeEvalConfig(body, "schema-test")
			if runtimeErr == nil {
				runtimeErr = cfg.Validate()
			}
			assert.Equal(t, tc.valid, schemaErr == nil, "schema: %v", schemaErr)
			assert.Equal(t, tc.valid, runtimeErr == nil, "runtime: %v", runtimeErr)
		})
	}
}

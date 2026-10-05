// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"maps"
	"net/http"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExplicitLocalPrimaryInteractionsMatchDatasetValidation(t *testing.T) {
	for _, field := range []string{"query", "response", "messages"} {
		for _, value := range []string{`""`, `" \t\n\u00a0"`, `null`, `42`, `{}`, `[]`, `"valid"`} {
			for _, caller := range []string{"run", "create", "up"} {
				t.Run(field+"/"+value+"/"+caller, func(t *testing.T) {
					rows := `{"input":"valid"}` + "\n" + `{"input":` + value + "}\n"
					dir := localSourceConfig(t, rows, 1)
					editLocalSourceConfig(t, dir, func(eval map[string]any) {
						eval["evaluators"] = []any{map[string]any{
							"evaluator":    "builtin.relevance",
							"data_mapping": map[string]string{field: "{{item.input}}"},
						}}
						if field == "messages" {
							eval["evaluation_level"] = project.EvaluationLevelConversation
						}
					})
					ec, requests := localSourceContext(t, func(definition map[string]any) {
						definition["data_source_config"] = map[string]any{
							"type": "custom", "item_schema": map[string]any{
								"type": "object", "properties": map[string]any{"input": map[string]any{}},
								"required": []string{"input"},
							},
						}
						definition["testing_criteria"] = []any{map[string]any{
							"name": "relevance", "evaluator_name": "builtin.relevance",
							"data_mapping": map[string]string{field: "{{item.input}}"},
						}}
					})
					ec.schemas["builtin.relevance"].Definition.DataSchema = &eval_api.JSONSchema{
						Type: "object", Required: []string{field},
						Properties: map[string]any{field: map[string]any{}},
					}
					before := maps.Clone(ec.state)
					var err error
					if caller == "run" {
						_, err = startLocalSource(t, ec, dir, "local-quality", nil)
					} else {
						cfg, loadErr := project.OpenEvalConfig(dir)
						require.NoError(t, loadErr)
						err = reconcileArtifactConfig(t, caller, ec, cfg, dir)
					}
					invalid := value != `[]` && value != `"valid"`
					recorded := recordedIdentityRequests(requests)
					if invalid {
						require.ErrorContains(t, err, `"input"`)
						require.ErrorContains(t, err, "non-empty")
						for _, request := range recorded {
							assert.Equal(t, http.MethodGet, request.method, "invalid rows must never publish or submit")
						}
						assert.Equal(t, before, ec.state)
					} else {
						require.NoError(t, err, "empty arrays and nonempty strings remain valid")
						assert.NotEmpty(t, recorded)
					}
				})
			}
		}
	}
}

func TestExplicitLocalStoredPrimaryInteractionRejectsWhitespace(t *testing.T) {
	dir := localSourceConfig(t, `{"query":"valid","transcript":" \t"}`+"\n", 0)
	ec, requests := localSourceContext(t, func(definition map[string]any) {
		definition["data_source_config"] = map[string]any{
			"type": "custom", "item_schema": map[string]any{
				"type": "object", "properties": map[string]any{
					"query": map[string]any{"type": "string"}, "transcript": map[string]any{"type": "string"},
				},
			},
		}
		definition["testing_criteria"] = []any{map[string]any{
			"name": "stored", "evaluator_name": "custom",
			"data_mapping": map[string]string{"messages": "{{item.transcript}}"},
		}}
	})
	before := maps.Clone(ec.state)
	output, err := startLocalSource(t, ec, dir, "local-quality", nil)
	require.ErrorContains(t, err, `"transcript"`)
	require.ErrorContains(t, err, "local row 1")
	assert.Empty(t, output)
	assert.Equal(t, before, ec.state)
	recorded := recordedIdentityRequests(requests)
	require.NotEmpty(t, recorded, "the stored criterion was fetched")
	for _, request := range recorded {
		assert.Equal(t, http.MethodGet, request.method, "stored mappings must be validated before submission")
	}
}

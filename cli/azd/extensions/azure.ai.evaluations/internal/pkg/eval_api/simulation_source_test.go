// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The request shape the service reads. A simulation run is not target
// completions, and it carries no {{item.query}}: a seed row describes a
// conversation to have, not a question to ask. ADO 5631478.
func TestNewSimulationDataSource_WireShape(t *testing.T) {
	t.Parallel()

	ds := NewSimulationDataSource("hero-agent", "gpt-4o-mini", 1, 5)
	ds.SetFileID("azureai://accounts/acct/data/seeds/versions/1.0")

	body, err := json.Marshal(ds)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))

	assert.Equal(t, "azure_ai_user_conversation_simulation_preview", decoded["type"])

	source, ok := decoded["source"].(map[string]any)
	require.True(t, ok, "the seed dataset is referenced by id")
	assert.Equal(t, "file_id", source["type"])
	assert.Equal(t, "azureai://accounts/acct/data/seeds/versions/1.0", source["id"])

	target, ok := decoded["target"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "azure_ai_agent", target["type"])
	assert.Equal(t, "hero-agent", target["name"])

	model, ok := decoded["model_configuration"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "gpt-4o-mini", model["model"])

	sim, ok := decoded["default_simulation_configuration"].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, 1, sim["conversation_repetitions"])
	assert.EqualValues(t, 5, sim["max_num_turns"])
	assert.Equal(t, false, sim["enable_conversation_dataset_generation"],
		"azd's workflow is staged: a run consumes seeds, it does not generate them")
	assert.Equal(t, map[string]any{
		"test_case_description":    "test_case_description",
		"simulation_configuration": "simulation_configuration",
	}, decoded["data_mapping"], "simulation mappings name columns, not item templates")
}

// Binding a question is what made an agent answer an empty one. A simulation
// data source must never carry a template.
func TestNewSimulationDataSource_BindsNoQuestionTemplate(t *testing.T) {
	t.Parallel()

	ds := NewSimulationDataSource("hero-agent", "gpt-4o-mini", 1, 5)

	assert.Nil(t, ds.InputMessages, "a seed row has no question on it to bind")
	assert.Empty(t, ds.TemplateItemFields())
	assert.Empty(t, ds.MissingTemplateFields([]map[string]any{
		{"test_case_description": "A customer asks about a delayed order."},
	}), "seed rows satisfy a simulation source as they are")
}

// An omitted turn bound leaves the service's default rather than truncating a
// conversation at a number nobody chose.
func TestNewSimulationDataSource_OmitsAnUnstatedTurnBound(t *testing.T) {
	t.Parallel()

	body, err := json.Marshal(NewSimulationDataSource("hero-agent", "gpt-4o-mini", 1, 0))
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))

	sim, ok := decoded["default_simulation_configuration"].(map[string]any)
	require.True(t, ok)
	_, present := sim["max_num_turns"]
	assert.False(t, present, "an unstated bound is absent, not zero")

	// The generation flag is a decision, so it is sent even when false.
	assert.Equal(t, false, sim["enable_conversation_dataset_generation"])
}

// The turn-level data sources must keep their own discriminator: falling back
// to target completions is what the spec forbids.
func TestSimulationDiscriminatorIsDistinctFromTargetCompletions(t *testing.T) {
	t.Parallel()

	assert.NotEqual(t, EvalRunDataSourceTypeAgentTarget, EvalRunDataSourceTypeUserConversationSimulation)
	assert.Equal(t, EvalRunDataSourceTypeAgentTarget,
		NewAgentTargetDataSource("hero-agent", nil).Type)
	assert.Equal(t, EvalRunDataSourceTypeUserConversationSimulation,
		NewSimulationDataSource("hero-agent", "gpt-4o-mini", 1, 5).Type)
	assert.Nil(t, NewAgentTargetDataSource("hero-agent", nil).DataMapping)
	assert.Nil(t, NewDatasetOnlyDataSource().DataMapping)
}

func TestSimulationDataMappingSurvivesReadback(t *testing.T) {
	const body = `{
		"type":"azure_ai_user_conversation_simulation_preview",
		"source":{"type":"file_id","id":"issued-id"},
		"data_mapping":{"test_case_description":"scenario","simulation_configuration":"settings"}
	}`
	var source EvalRunDataSource
	require.NoError(t, json.Unmarshal([]byte(body), &source))
	require.Equal(t, map[string]string{
		"test_case_description": "scenario", "simulation_configuration": "settings",
	}, source.DataMapping)
	encoded, err := json.Marshal(source)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, map[string]any{
		"test_case_description": "scenario", "simulation_configuration": "settings",
	}, decoded["data_mapping"], "reusing a run must retain non-default column mappings")
}

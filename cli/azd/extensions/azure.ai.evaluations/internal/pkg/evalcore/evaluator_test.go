// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package evalcore

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// The service-target provider receives the config as JSON, not YAML, so both
// paths have to decode the mapping form identically. Supporting only YAML made
// `azd deploy` fail on a config the CLI itself writes.
func TestEvaluatorListDecodesEntriesFromJSON(t *testing.T) {
	const payload = `[
		{"evaluator": "builtin.task_adherence"},
		{"evaluator": "support-quality", "name": "quality_strict",
		 "initialization_parameters": {"model": "gpt-5.6-luna", "threshold": 4}},
		{"evaluator": "pinned", "version": "3"}
	]`

	var list EvaluatorList
	require.NoError(t, json.Unmarshal([]byte(payload), &list))
	require.Len(t, list, 3)

	require.Equal(t, "builtin.task_adherence", list[0].Evaluator)
	require.True(t, list[0].IsBuiltin())
	require.Equal(t, "task_adherence", list[0].APIName())
	require.Equal(t, "task_adherence", list[0].CriterionName())
	require.Nil(t, list[0].InitializationParameters)

	require.Equal(t, "support-quality", list[1].Evaluator)
	require.Equal(t, "quality_strict", list[1].CriterionName())
	require.False(t, list[1].IsBuiltin())
	require.Equal(t, "gpt-5.6-luna", list[1].InitializationParameters["model"])
	require.EqualValues(t, 4, list[1].InitializationParameters["threshold"])

	require.Equal(t, "pinned", list[2].Evaluator)
	require.Equal(t, "3", list[2].Version)
}

// The JSON and YAML decoders must agree, otherwise a config behaves one way
// through the CLI and another through `azd up`.
func TestEvaluatorListJSONMatchesYAML(t *testing.T) {
	const doc = `
- evaluator: builtin.task_adherence
- evaluator: support-quality
  name: quality_strict
  initialization_parameters:
    model: gpt-5.6-luna
  data_mapping:
    query: "{{item.customer_message}}"
`
	var fromYAML EvaluatorList
	require.NoError(t, yaml.Unmarshal([]byte(doc), &fromYAML))

	encoded, err := json.Marshal(fromYAML)
	require.NoError(t, err)

	var fromJSON EvaluatorList
	require.NoError(t, json.Unmarshal(encoded, &fromJSON))
	require.Equal(t, fromYAML, fromJSON)
}

// A bare string is the old shorthand. It has to be refused with the remedy
// rather than a decoder type error, through both decoders, because the
// service-target provider only ever sees JSON.
func TestEvaluatorListRefusesBareString(t *testing.T) {
	t.Run("yaml", func(t *testing.T) {
		var list EvaluatorList
		err := yaml.Unmarshal([]byte("- builtin.task_adherence\n"), &list)
		require.Error(t, err)
		require.Contains(t, err.Error(), "- evaluator: builtin.task_adherence")
	})

	t.Run("json", func(t *testing.T) {
		var list EvaluatorList
		err := json.Unmarshal([]byte(`["builtin.task_adherence"]`), &list)
		require.Error(t, err)
		require.Contains(t, err.Error(), "- evaluator: builtin.task_adherence")
	})
}

func TestEvaluatorListRejectsEntryWithoutEvaluator(t *testing.T) {
	t.Run("yaml", func(t *testing.T) {
		var list EvaluatorList
		err := yaml.Unmarshal([]byte("- name: quality_strict\n"), &list)
		require.Error(t, err)
		require.Contains(t, err.Error(), "evaluator")
	})

	t.Run("json", func(t *testing.T) {
		var list EvaluatorList
		err := json.Unmarshal([]byte(`[{"name": "quality_strict"}]`), &list)
		require.Error(t, err)
		require.Contains(t, err.Error(), "evaluator")
	})
}

// The eval fingerprint is taken over this encoding, so a field the encoder
// drops is a change the reconciler cannot see.
func TestEvaluatorListMarshalKeepsEveryField(t *testing.T) {
	list := EvaluatorList{{
		Evaluator:                "support-quality",
		Name:                     "quality_strict",
		Version:                  "2",
		InitializationParameters: map[string]any{"model": "gpt-5.6-luna"},
		DataMapping:              map[string]string{"query": "{{item.customer_message}}"},
	}}

	encoded, err := json.Marshal(list)
	require.NoError(t, err)

	var round EvaluatorList
	require.NoError(t, json.Unmarshal(encoded, &round))
	require.Equal(t, list, round)
}

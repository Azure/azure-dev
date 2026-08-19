// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package evalcore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yaml "go.yaml.in/yaml/v3"
)

// decodeEvaluators reads an evaluators: block the way the config reader does.
func decodeEvaluators(t *testing.T, doc string) (EvaluatorList, error) {
	t.Helper()

	var holder struct {
		Evaluators EvaluatorList `yaml:"evaluators"`
	}
	err := yaml.Unmarshal([]byte(doc), &holder)
	return holder.Evaluators, err
}

// An anchor shares one judge configuration across entries, which is the reason
// to write one. Decoding an entry on its own lifts it away from the anchor, so
// this is the case that broke when the entry gained a strict decoder.
func TestAnAnchorSharedBetweenEvaluatorsResolves(t *testing.T) {
	list, err := decodeEvaluators(t, `
evaluators:
  - evaluator: builtin.relevance
    initialization_parameters: &judge
      deployment_name: gpt-4o
  - evaluator: builtin.coherence
    initialization_parameters: *judge
`)

	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, map[string]any{"deployment_name": "gpt-4o"}, list[0].InitializationParameters)
	assert.Equal(t, map[string]any{"deployment_name": "gpt-4o"}, list[1].InitializationParameters,
		"the second entry should carry what the anchor holds")
}

// A merge key is the other half of the same feature: it inherits the anchored
// entry and overrides one key.
func TestAMergeKeyInheritsTheEntryItNames(t *testing.T) {
	list, err := decodeEvaluators(t, `
evaluators:
  - &base
    evaluator: builtin.relevance
    initialization_parameters:
      deployment_name: gpt-4o
  - <<: *base
    evaluator: builtin.coherence
`)

	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "builtin.coherence", list[1].Evaluator, "the override should win")
	assert.Equal(t, map[string]any{"deployment_name": "gpt-4o"}, list[1].InitializationParameters,
		"the inherited key should survive")
}

// Resolving aliases must not cost the strictness it was added around: a key
// nobody declared is still named, whether it is written out or inherited.
func TestAMisspeltKeyIsStillRefusedThroughAnAlias(t *testing.T) {
	tests := map[string]string{
		"written out": `
evaluators:
  - evaluator: builtin.relevance
    verison: 3
`,
		"inherited through a merge": `
evaluators:
  - &base
    evaluator: builtin.relevance
    verison: 3
  - <<: *base
    evaluator: builtin.coherence
`,
	}

	for name, doc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := decodeEvaluators(t, doc)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "verison", "the key they typed should be named")
		})
	}
}

// yaml permits an anchor holding its own alias. Expanding it has no end, so it
// has to be refused rather than followed.
func TestAnAnchorThatContainsItselfIsRefused(t *testing.T) {
	_, err := decodeEvaluators(t, `
evaluators:
  - evaluator: builtin.relevance
    data_mapping: &loop
      query: *loop
`)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `anchor "loop" refers to itself`,
		"the refusal should be ours, not yaml's own report of an anchor it could not find")
}

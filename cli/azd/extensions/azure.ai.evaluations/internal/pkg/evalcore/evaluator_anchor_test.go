// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package evalcore

import (
	"regexp"
	"strconv"
	"strings"
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
		// The first entry is clean on purpose. Decoding stops at the first
		// entry that fails, so a typo written into the anchor would satisfy
		// this without the merge ever being read.
		"inherited through a merge": `
evaluators:
  - &base
    evaluator: builtin.relevance
  - <<: *base
    evaluator: builtin.coherence
    verison: 3
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

// An entry that is itself an alias is the most natural thing an anchor is for.
// It used to be refused before the alias was ever looked at, and refused with
// yaml's own numeric kind: "must be a mapping, got 16".
func TestAnEntryThatIsItselfAnAliasResolves(t *testing.T) {
	list, err := decodeEvaluators(t, `
anchors:
  - &shared
    evaluator: builtin.relevance
    initialization_parameters:
      deployment_name: gpt-4o
evaluators:
  - *shared
  - evaluator: builtin.coherence
`)

	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "builtin.relevance", list[0].Evaluator)
	assert.Equal(t, map[string]any{"deployment_name": "gpt-4o"}, list[0].InitializationParameters)
}

// A kind the file cannot use is named in words. yaml.Kind is an unnamed uint32
// with no String method, so %v printed the bit value.
func TestARefusedEntryNamesTheShapeInWords(t *testing.T) {
	_, err := decodeEvaluators(t, `
evaluators:
  - - evaluator: builtin.relevance
`)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "a list")
	assert.NotContains(t, err.Error(), "16", "the reader is not reading yaml's enum")
}

// The line a typo is reported on has to be a line the reader can look at.
//
// Expanding an alias makes the snippet longer than the entry it came from, so
// an offset computed from the snippet lands further down the file -- on a
// different, valid entry, or past the end.
func TestAMisspeltKeyPointsAtTheEntryAndNotPastIt(t *testing.T) {
	doc := `evaluators:
  - evaluator: builtin.relevance
    initialization_parameters: &judge
      deployment_name: gpt-4o
      api_version: "2026-01-01"
      temperature: 0
  - evaluator: builtin.coherence
    initialization_parameters: *judge
    verison: 3
  - evaluator: builtin.fluency
  - evaluator: builtin.groundedness
  - evaluator: builtin.similarity
`
	_, err := decodeEvaluators(t, doc)

	require.Error(t, err)
	require.Contains(t, err.Error(), "verison")

	reported := reportedLine(t, err)
	assert.LessOrEqual(t, reported, len(strings.Split(doc, "\n")),
		"a line past the end of the file is no help at all")
	assert.Equal(t, 7, reported,
		"the entry holding the typo begins on line 7; naming another entry accuses code that is fine")
}

// Without an alias the snippet is line-for-line with the file, so the exact
// key keeps being named.
func TestAMisspeltKeyWithoutAnAliasStillNamesItsOwnLine(t *testing.T) {
	_, err := decodeEvaluators(t, `evaluators:
  - evaluator: builtin.relevance
  - evaluator: builtin.coherence
    verison: 3
`)

	require.Error(t, err)
	assert.Equal(t, 4, reportedLine(t, err), "the typo is on line 4")
}

// reportedLine reads the line number out of a decode error.
func reportedLine(t *testing.T, err error) int {
	t.Helper()
	match := regexp.MustCompile(`line (\d+):`).FindStringSubmatch(err.Error())
	require.Len(t, match, 2, "the error should name a line: %v", err)
	n, convErr := strconv.Atoi(match[1])
	require.NoError(t, convErr)
	return n
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

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Resolving a `$ref` round-trips the document through a map, which expands YAML
// anchors. The expansion has to be faithful, because the alias is how authors
// avoid repeating a judge model across evaluators.
func TestAnchorsSurviveRefResolution(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "evaluators"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "evaluators", "quality.json"),
		[]byte(`{"type":"rubric","dimensions":[{"id":"tone","weight":3}]}`),
		0o600))

	path := filepath.Join(dir, EvalConfigBase)
	require.NoError(t, os.WriteFile(path, []byte(`
evaluators:
  - $ref: ./evaluators/quality.json
    name: quality

evals:
  - name: nightly
    dataset: golden
    evaluators:
      - evaluator: builtin.relevance
        initialization_parameters: &judge
          model: gpt-5.6-luna
      - evaluator: builtin.coherence
        initialization_parameters: *judge
`), 0o600))

	cfg, err := LoadEvalConfig(path)
	require.NoError(t, err, "an anchor is not a mistyped key")
	require.Len(t, cfg.Evals, 1)
	require.Len(t, cfg.Evals[0].Evaluators, 2)

	assert.Equal(t, cfg.Evals[0].Evaluators[0].InitializationParameters,
		cfg.Evals[0].Evaluators[1].InitializationParameters,
		"the alias has to carry the same parameters the anchor declared")
	assert.NotEmpty(t, cfg.Evals[0].Evaluators[1].InitializationParameters,
		"an alias that expanded to nothing would silently drop the judge model")
}

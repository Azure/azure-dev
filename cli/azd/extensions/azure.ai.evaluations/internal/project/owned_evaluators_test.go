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

// An evaluator carrying its rubric is one this configuration owns, so it has to
// reach the publish loops.
//
// Both loops selected on `source` alone, and validation guarantees a rubric
// written under `definition` comes with no source. So a `$ref` to a rubric
// decoded, validated, reported nothing, and published nothing: the eval was then
// created against an evaluator the service had never been told about. Every test
// for that feature stopped at decoding, which is why none of them noticed.
func TestAnEvaluatorCarryingItsRubricIsPublished(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "evaluators"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "evaluators", "quality.json"),
		[]byte(`{"type":"rubric","dimensions":[{"id":"tone","weight":3}]}`),
		0o600))

	path := filepath.Join(dir, EvalConfigBase)
	require.NoError(t, os.WriteFile(path, []byte(`
evaluators:
  - name: quality
    definition:
      $ref: ./evaluators/quality.json
  - name: from-a-file
    source: ./evaluators/quality.json
  - name: builtin.relevance-is-not-ours
    version: "3"

evals:
  - name: nightly
    dataset: golden
`), 0o600))

	cfg, err := LoadEvalConfig(path)
	require.NoError(t, err)

	var owned []string
	for _, decl := range cfg.CustomEvaluators() {
		owned = append(owned, decl.Name)
	}

	assert.Contains(t, owned, "quality",
		"a rubric this configuration carries is one it owns, so it must be published")
	assert.Contains(t, owned, "from-a-file")
	assert.NotContains(t, owned, "builtin.relevance-is-not-ours",
		"an entry that only pins a registered version has nothing to publish")
}

// The rescue is gated on the document actually using `$ref`, and both routes
// have to gate on it the same way.
//
// The CLI returns a configuration with no `$ref` untouched so the decoder keeps
// its own line numbers. That fast path skipped the rescue while the deploy route
// still applied it, so a hand-written entry carrying rubric keys deployed and was
// then refused by every command that read it -- the same asymmetry twice over.
func TestRubricKeysWithoutARefAreRefusedOnBothRoutes(t *testing.T) {
	dir := t.TempDir()

	body := `
evaluators:
  - name: quality
    type: rubric
    dimensions:
      - id: tone
        weight: 3

evals:
  - name: nightly
    dataset: golden
`
	path := filepath.Join(dir, EvalConfigBase)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	_, fromDisk := LoadEvalConfig(path)
	require.Error(t, fromDisk, "rubric keys nobody spliced are a mistake, not a rubric")
	assert.Contains(t, fromDisk.Error(), "dimensions")

	svc := serviceWith(t, map[string]any{
		"evaluators": []any{map[string]any{
			"name": "quality",
			"type": "rubric",
			"dimensions": []any{
				map[string]any{"id": "tone", "weight": 3},
			},
		}},
		"evals": []any{map[string]any{"name": "nightly", "dataset": "golden"}},
	})
	_, fromService := EvalConfigFromService(svc, dir)
	require.Error(t, fromService, "`azd up` has to refuse what every command refuses")
	assert.Contains(t, fromService.Error(), "dimensions")
}

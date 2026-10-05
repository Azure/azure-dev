// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/pkg/evalcore"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Scaffolding an eval edits the file instead of rewriting it.
//
// `init` used to marshal the whole decoded configuration back, which deleted
// every comment the author had written and dropped any key the structs do not
// model. It now writes only the entries it decided to add.
func TestScaffoldingAnEvalLeavesTheRestOfTheFileAlone(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, EvalConfigBase), []byte(`# Owned by the support team.
datasets:
  # curated by hand -- do not regenerate
  - name: golden
    file: ./datasets/golden.jsonl

evals:
  - name: existing
    dataset: golden
    target:
      $ref: ./parts/target.yaml
`), 0o600))

	require.NoError(t, ApplyScaffold(dir, ScaffoldWrite{
		Evaluators: []EvaluatorDecl{{Name: "quality", Source: "./evaluators/quality.json"}},
		Evals: []Eval{{
			Name:       "nightly",
			Dataset:    "golden",
			Evaluators: evalcore.EvaluatorList{{Evaluator: "quality"}},
		}},
	}))

	got, err := os.ReadFile(filepath.Join(dir, EvalConfigBase))
	require.NoError(t, err)
	body := string(got)

	assert.Contains(t, body, "# Owned by the support team.")
	assert.Contains(t, body, "# curated by hand -- do not regenerate")
	assert.Contains(t, body, "$ref: ./parts/target.yaml",
		"a directive on a shape these structs do not model survives")

	assert.Contains(t, body, "name: nightly")
	assert.Contains(t, body, "name: quality")
	assert.Contains(t, body, "name: existing", "the eval that was already there is untouched")
}

// The writer cannot express a removal, so no caller can talk it into replacing
// an eval a reader tuned by hand.
func TestScaffoldingCannotRemoveAnEval(t *testing.T) {
	dir := t.TempDir()
	original := `evals:
  - name: keep-me
    dataset: golden
  - name: nightly
    dataset: stale
    max_samples: 5
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, EvalConfigBase), []byte(original), 0o600))

	require.NoError(t, ApplyScaffold(dir, ScaffoldWrite{
		Evals: []Eval{{Name: "added", Dataset: "golden"}},
	}))

	cfg, err := OpenEvalConfig(dir)
	require.NoError(t, err)
	require.Len(t, cfg.Evals, 3, "the two that were there plus the one added")

	kept, err := cfg.Eval("nightly")
	require.NoError(t, err)
	assert.Equal(t, "stale", kept.Dataset)
	assert.Equal(t, 5, kept.MaxSamples, "every key of the existing eval survives")
}

// A scaffold that decided to add nothing does not touch the file at all.
func TestScaffoldingNothingDoesNotRewriteTheFile(t *testing.T) {
	original := "evals:\n  - name: nightly\n    dataset: golden\n"
	dir := t.TempDir()
	path := filepath.Join(dir, EvalConfigBase)
	require.NoError(t, os.WriteFile(path, []byte(original), 0o600))

	require.NoError(t, ApplyScaffold(dir, ScaffoldWrite{}))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, original, string(after), "untouched, byte for byte")
}

// A composite evaluator reference is opaque to ApplyScaffold like any other
// evaluator name, so an eval that already names one alongside a deliberately
// retained constituent and a custom evaluator — each with its own label,
// version, initialization parameters and data mapping — must survive an
// unrelated append exactly as written. ApplyScaffold must not expand, dedupe,
// or rewrite evaluator entries it did not add.
func TestScaffoldingPreservesACompositeConstituentAndCustomEvaluatorBlock(t *testing.T) {
	dir := t.TempDir()
	original := `# Authored choices: preserve labels, versions and mappings.
datasets:
  - name: fixture-rows
evaluators:
  - name: fixture-quality
evals:
  - name: authored-quality
    dataset: fixture-rows
    evaluation_level: turn
    evaluators:
      - evaluator: builtin.output_quality
        name: output-strict
        version: "17"
        initialization_parameters:
          model: fixture-judge
        data_mapping:
          query: "{{item.original_query}}"
          response: "{{item.original_response}}"
      # Deliberately retained by the author, even though covered by the composite.
      - evaluator: builtin.coherence
        name: coherence-strict
        version: "3"
        initialization_parameters:
          model: fixture-judge
          threshold: 4
      - evaluator: fixture-quality
        name: custom-policy
        version: "7"
        initialization_parameters:
          model: fixture-judge
          threshold: 0.85
        data_mapping:
          query: "{{item.original_query}}"
          response: "{{item.original_response}}"
`
	path := filepath.Join(dir, EvalConfigBase)
	require.NoError(t, os.WriteFile(path, []byte(original), 0o600))

	require.NoError(t, ApplyScaffold(dir, ScaffoldWrite{
		Evals: []Eval{{
			Name:       "added-quality",
			Dataset:    "fixture-rows",
			Evaluators: evalcore.EvaluatorList{{Evaluator: "builtin.task_completion"}},
		}},
	}))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	body := string(got)

	// The entire original document is an exact byte prefix of the result.
	assert.True(t, strings.HasPrefix(body, original),
		"the original composite/constituent/custom block must survive untouched")

	cfg, err := OpenEvalConfig(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"authored-quality", "added-quality"}, cfg.EvalNames())

	authored, err := cfg.Eval("authored-quality")
	require.NoError(t, err)
	require.Len(t, authored.Evaluators, 3, "no constituent or default was implicitly added or removed")
	assert.Equal(t, "builtin.output_quality", authored.Evaluators[0].Evaluator)
	assert.Equal(t, "output-strict", authored.Evaluators[0].Name)
	assert.Equal(t, "17", authored.Evaluators[0].Version)
	assert.Equal(t, "builtin.coherence", authored.Evaluators[1].Evaluator)
	assert.Equal(t, "coherence-strict", authored.Evaluators[1].Name)
	assert.Equal(t, "fixture-quality", authored.Evaluators[2].Evaluator)
	assert.Equal(t, "custom-policy", authored.Evaluators[2].Name)

	added, err := cfg.Eval("added-quality")
	require.NoError(t, err)
	require.Len(t, added.Evaluators, 1)
	assert.Equal(t, "builtin.task_completion", added.Evaluators[0].Evaluator)
}

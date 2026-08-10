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

// `init` and `generate` both read the configuration, add an entry and write the
// whole file back. Anything the round trip cannot carry is deleted from a file
// the developer wrote and is expected to keep editing.
func TestEvalConfigRoundTripKeepsWhatTheAuthorWrote(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, EvalConfigBase)

	authored := `evals:
  - name: support-agent-eval
    target:
      type: agent
      name: support-agent
    dataset: golden
    evaluators:
      - evaluator: builtin.task_adherence
datasets:
  - name: golden
    source: ./datasets/golden.jsonl
`
	require.NoError(t, os.WriteFile(path, []byte(authored), 0o600))

	cfg, err := LoadEvalConfig(path)
	require.NoError(t, err)
	require.NoError(t, SaveEvalConfigTo(path, cfg))

	reloaded, err := LoadEvalConfig(path)
	require.NoError(t, err)

	require.Len(t, reloaded.Evals, 1)
	assert.Equal(t, "support-agent-eval", reloaded.Evals[0].Name)
	require.NotNil(t, reloaded.Evals[0].Target, "the target survived the rewrite")
	assert.Equal(t, "agent", reloaded.Evals[0].Target.Type)
	assert.Equal(t, "support-agent", reloaded.Evals[0].Target.Name)
	require.Len(t, reloaded.Datasets, 1)
	assert.Equal(t, "golden", reloaded.Datasets[0].Name)
}

// A hand-edited configuration is the normal way to use this file, and a
// misspelled key used to be read as nothing at all: `agent: support-agent`
// under `target:` left an empty target, and the run failed later with a message
// about the target rather than about the typo that caused it.
func TestEvalConfigRefusesAKeyItDoesNotKnow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, EvalConfigBase)

	require.NoError(t, os.WriteFile(path, []byte(`evals:
  - name: support-agent-eval
    target:
      agent: support-agent
`), 0o600))

	_, err := LoadEvalConfig(path)

	require.Error(t, err, "a key the extension does not know is a typo, not a no-op")
	assert.Contains(t, err.Error(), "agent")
}

// The keys the extension does know must still load, or strictness would break
// every configuration it writes itself.
func TestEvalConfigAcceptsEveryKeyItWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, EvalConfigBase)

	require.NoError(t, os.WriteFile(path, []byte(`datasets:
  - name: golden
    source: ./datasets/golden.jsonl
    version: "2"
evaluators:
  - name: quality
    source: ./evaluators/quality.json
evals:
  - name: e
    id: eval_1
    description: grades support answers
    dataset: golden
    evaluation_level: turn
    max_samples: 15
    evaluators:
      - evaluator: builtin.task_adherence
    target:
      type: agent
      name: support-agent
`), 0o600))

	cfg, err := LoadEvalConfig(path)

	require.NoError(t, err)
	require.Len(t, cfg.Evals, 1)
	assert.Equal(t, 15, cfg.Evals[0].MaxSamples)
}

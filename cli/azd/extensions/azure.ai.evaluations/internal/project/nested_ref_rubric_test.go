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

// The documented layout puts the whole eval config behind a `$ref` on the
// service, and the evaluators inside it carry `$ref`s of their own.
func TestARefdRubricInsideARefdConfig(t *testing.T) {
	dir := t.TempDir()
	evals := filepath.Join(dir, "evals")
	require.NoError(t, os.MkdirAll(filepath.Join(evals, "evaluators"), 0o755))

	require.NoError(t, os.WriteFile(
		filepath.Join(evals, "evaluators", "quality.json"),
		[]byte(`{"type":"rubric","dimensions":[{"id":"tone","weight":3}]}`),
		0o600))

	require.NoError(t, os.WriteFile(
		filepath.Join(evals, EvalConfigBase), []byte(`
evaluators:
  - $ref: ./evaluators/quality.json
    name: quality

evals:
  - name: nightly
    dataset: golden
`), 0o600))

	svc := serviceWith(t, map[string]any{"$ref": "./evals/" + EvalConfigBase})

	cfg, err := EvalConfigFromService(svc, dir)
	require.NoError(t, err, "the layout the README and spec document has to deploy")
	require.Len(t, cfg.Evaluators, 1)
	assert.Equal(t, "quality", cfg.Evaluators[0].Name)
	assert.Equal(t, "rubric", cfg.Evaluators[0].Definition["type"])
}

// `dimensions` is what tells a spliced rubric from a mistake, so a `$ref` to a
// file that is not a rubric is still reported rather than filed away.
//
// This is the whole difference between the rescue and a catch-all, so it is
// worth a test of its own: widening the gate would make every misspelling in an
// evaluator entry publishable content.
func TestARefToSomethingThatIsNotARubricIsStillRejected(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "evaluators"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "evaluators", "quality.json"),
		[]byte(`{"nmae":"quality","weight":3}`), 0o600))

	path := filepath.Join(dir, EvalConfigBase)
	require.NoError(t, os.WriteFile(path, []byte(`
evaluators:
  - $ref: ./evaluators/quality.json

evals:
  - name: nightly
    dataset: golden
`), 0o600))

	_, err := LoadEvalConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nmae", "the error has to name the key that is wrong")
}

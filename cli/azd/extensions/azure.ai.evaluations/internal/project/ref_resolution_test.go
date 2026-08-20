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

// `$ref` means the same thing to `azd up` and to the CLI commands.
//
// Core owns the resolver but does not run it for us -- it hands each extension
// the entry with `$ref` still in it. The service target called it and this path
// did not, so an include deployed fine and then failed every `azd ai eval`
// command with `unknown key "$ref"`: one file, two meanings, decided by which
// command opened it.
func TestRefResolvesOnTheCLIPathToo(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "evaluators"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "evaluators", "quality.yaml"),
		[]byte("name: support-agent-quality\nsource: ./quality.json\n"),
		0o600))

	path := filepath.Join(dir, "azure.eval.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
datasets:
  - name: golden
    file: ./datasets/golden.jsonl

evaluators:
  - $ref: ./evaluators/quality.yaml

evals:
  - name: nightly
    dataset: golden
`), 0o600))

	cfg, err := LoadEvalConfig(path)
	require.NoError(t, err, "the include the service target resolves has to resolve here too")
	require.Len(t, cfg.Evaluators, 1)
	assert.Equal(t, "support-agent-quality", cfg.Evaluators[0].Name,
		"the referenced file's content replaces the directive")
	// Verbatim, deliberately: core rebases only the two path keys it owns, so a
	// relative `source:` written beside the referenced file arrives unchanged and
	// is then resolved against azure.eval.yaml. That is a known limitation, not
	// the behaviour this asserts -- carrying the rubric under `definition:`
	// avoids it, and EvaluatorNotGeneratedYet names it when the path misses.
	assert.Equal(t, "./quality.json", cfg.Evaluators[0].Source,
		"a spliced path is not rebased; see the note above before changing this")
}

// A `$ref` can name the rubric itself, not only a pointer to one.
//
// This is the shape the spec documents, and it works because resolution splices
// the referenced file's keys into the entry: they have to land on fields of the
// declaration or strict decoding rejects them. `definition` is that field.
func TestRefCanNameTheRubricItself(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "evaluators"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "evaluators", "quality.json"),
		[]byte(`{"name":"support-agent-quality",`+
			`"definition":{"type":"rubric","dimensions":[{"name":"tone","weight":1}]}}`),
		0o600))

	path := filepath.Join(dir, "azure.eval.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
evaluators:
  - $ref: ./evaluators/quality.json

evals:
  - name: nightly
    dataset: golden
`), 0o600))

	cfg, err := LoadEvalConfig(path)
	require.NoError(t, err, "a rubric named by $ref has to decode")
	require.Len(t, cfg.Evaluators, 1)
	assert.Equal(t, "support-agent-quality", cfg.Evaluators[0].Name)
	assert.Equal(t, "rubric", cfg.Evaluators[0].Definition["type"],
		"the rubric travels with the declaration, so there is no second file to find")
	assert.Empty(t, cfg.Evaluators[0].Source,
		"a definition in hand is not a path to resolve against anything")
}

// Both routes into a configuration have to agree about a `$ref`'d rubric.
//
// The deploy path resolves includes itself rather than going through
// LoadEvalConfig, so a rescue added on one side only would recreate the exact
// asymmetry that started this work -- an include `azd up` accepted and every
// CLI command refused, in mirror image.
func TestBothRoutesReadARefdRubricTheSameWay(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "evaluators"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "evaluators", "quality.json"),
		[]byte(`{"type":"rubric","dimensions":[{"id":"tone","weight":3}]}`),
		0o600))

	path := filepath.Join(dir, "azure.eval.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
evaluators:
  - $ref: ./evaluators/quality.json
    name: quality

evals:
  - name: nightly
    dataset: golden
`), 0o600))

	fromDisk, err := LoadEvalConfig(path)
	require.NoError(t, err)

	svc := serviceWith(t, map[string]any{
		"evaluators": []any{map[string]any{
			"$ref": "./evaluators/quality.json",
			"name": "quality",
		}},
		"evals": []any{map[string]any{"name": "nightly", "dataset": "golden"}},
	})
	fromService, err := EvalConfigFromService(svc, dir)
	require.NoError(t, err, "`azd up` has to read what the CLI reads")

	assert.Equal(t, fromDisk.Evaluators, fromService.Evaluators,
		"one file, one meaning, whichever command opened it")
}

// A `$ref` can name a bare rubric file, which is the shape the spec documents
// and the shape `generate` downloads from the service.
//
// `$ref` splices the file's top-level keys into the entry, so `dimensions` and
// friends land beside `name` and used to be rejected outright. They are moved
// under `definition` instead. Wrapping the file would have been the smaller
// change and the wrong one: the tool writes that file, so the config has to
// read what the tool writes.
func TestRefCanNameABareRubricFile(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "evaluators"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "evaluators", "support-agent-quality.json"),
		[]byte(`{"type":"rubric","pass_threshold":0.7,`+
			`"dimensions":[{"id":"resolves_issue","weight":9,"description":"Resolves it."}]}`),
		0o600))

	path := filepath.Join(dir, "azure.eval.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
evaluators:
  - $ref: ./evaluators/support-agent-quality.json
    name: support-agent-quality

evals:
  - name: nightly
    dataset: golden
`), 0o600))

	cfg, err := LoadEvalConfig(path)
	require.NoError(t, err, "the spec's own example has to load")
	require.Len(t, cfg.Evaluators, 1)
	assert.Equal(t, "support-agent-quality", cfg.Evaluators[0].Name,
		"the sibling name stays the author's, not a key from the rubric")
	assert.Equal(t, "rubric", cfg.Evaluators[0].Definition["type"])
	assert.Equal(t, 0.7, cfg.Evaluators[0].Definition["pass_threshold"],
		"every rubric key travels, not just the ones this decoder happens to know")
	assert.Len(t, cfg.Evaluators[0].Definition["dimensions"], 1)
}

// A configuration that uses no `$ref` is never rescued, so a misspelling in a
// hand-written entry is reported rather than filed away.
//
// The `dimensions` gate is what separates the rescue from a catch-all, and it is
// exercised in nested_ref_rubric_test.go; this pins the other half, that a
// document nobody spliced into is left strictly alone.
func TestAMisspelledEvaluatorKeyIsStillRejected(t *testing.T) {
	dir := t.TempDir()

	path := filepath.Join(dir, "azure.eval.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
evaluators:
  - nmae: support-agent-quality
    definition:
      type: rubric
`), 0o600))

	_, err := LoadEvalConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nmae", "the error has to name the key that is wrong")
}

// Sibling keys overlay the loaded file, which is what lets a name live in the
// configuration while the definition it names lives beside the code it grades.
func TestRefSiblingKeysOverlayTheLoadedFile(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "evaluators"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "evaluators", "quality.yaml"),
		[]byte("name: from-the-file\nsource: ./quality.json\n"),
		0o600))

	path := filepath.Join(dir, "azure.eval.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
evaluators:
  - $ref: ./evaluators/quality.yaml
    name: from-the-configuration

evals:
  - name: nightly
`), 0o600))

	cfg, err := LoadEvalConfig(path)
	require.NoError(t, err)
	require.Len(t, cfg.Evaluators, 1)
	assert.Equal(t, "from-the-configuration", cfg.Evaluators[0].Name)
}

// A configuration with no include is handed to the decoder untouched, so its
// diagnostics keep the line numbers of the file the author actually wrote.
func TestConfigWithoutRefIsNotRoundTripped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "azure.eval.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
datasets:
  - name: golden
    fiel: ./datasets/golden.jsonl
`), 0o600))

	_, err := LoadEvalConfig(path)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "line 4",
		"a typo is reported where it was written, not where a re-marshal put it")
}

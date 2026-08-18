// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Hand-editing the file is the documented way to use one, so a mistyped key has
// to read as a mistyped key rather than as a Go type the reader has never seen.
func TestExplainUnknownKeys(t *testing.T) {
	got := explainUnknownKeys(errors.New(
		"yaml: unmarshal errors:\n  line 7: field evaulators not found in type project.Eval")).Error()

	assert.Contains(t, got, `unknown key "evaulators"`)
	assert.Contains(t, got, `did you mean "evaluators"?`)
	assert.Contains(t, got, "line 7", "the line number is the useful half of what yaml said")
	assert.NotContains(t, got, "project.Eval", "the Go type is an implementation detail")
}

// Nothing close enough is worth suggesting; a wrong suggestion is worse than
// none.
func TestExplainUnknownKeys_NoNearMiss(t *testing.T) {
	got := explainUnknownKeys(errors.New(
		"yaml: unmarshal errors:\n  line 3: field banana not found in type project.Eval")).Error()

	assert.Contains(t, got, `unknown key "banana"`)
	assert.NotContains(t, got, "did you mean")
}

// Anything that is not an unknown-key failure is passed through untouched.
func TestExplainUnknownKeys_LeavesOtherErrors(t *testing.T) {
	original := errors.New("yaml: line 2: did not find expected key")
	assert.Equal(t, original, explainUnknownKeys(original))
}

func TestKeysOfTypeCoversTheDeclarations(t *testing.T) {
	assert.Contains(t, keysOfType("project.Eval"), "evaluators")
	assert.Contains(t, keysOfType("project.EvalConfig"), "datasets")
	assert.Contains(t, keysOfType("project.DatasetDecl"), "source")
	assert.Empty(t, keysOfType("project.Unknown"))
}

// `azd ai agent eval` writes an eval.yaml of its own with an entirely different
// shape. Suggesting a near-miss for each of its keys in turn would walk the
// reader into rewriting another tool's file a line at a time.
//
// Its `evaluators` key happens to share a name with ours, so the mismatch
// inside it reports against the nested type. That must not disqualify the check.
func TestExplainUnknownKeys_AnotherToolsFile(t *testing.T) {
	got := explainUnknownKeys(errors.New(
		"yaml: unmarshal errors:\n" +
			"  line 1: field name not found in type project.EvalConfig\n" +
			"  line 2: field agent not found in type project.EvalConfig\n" +
			"  line 6: field dataset not found in type project.EvalConfig\n" +
			"  line 13: field local_uri not found in type project.EvaluatorDecl\n" +
			"  line 14: field options not found in type project.EvalConfig\n" +
			"  line 16: field max_samples not found in type project.EvalConfig")).Error()

	assert.Contains(t, got, "not one")
	assert.Contains(t, got, "azd ai agent eval run", "the reader is told where the file does belong")
	assert.NotContains(t, got, "did you mean",
		"suggesting a fix per key sends the reader down the wrong path entirely")
}

// One stray key beside recognized ones is still a typo, so the suggestion stands.
func TestExplainUnknownKeys_OneStrayTopLevelKey(t *testing.T) {
	got := explainUnknownKeys(errors.New(
		"yaml: unmarshal errors:\n  line 1: field datsets not found in type project.EvalConfig")).Error()

	assert.Contains(t, got, `did you mean "datasets"?`)
	assert.NotContains(t, got, "does not look like")
}

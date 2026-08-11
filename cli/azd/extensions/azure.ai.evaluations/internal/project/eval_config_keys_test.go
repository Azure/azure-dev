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

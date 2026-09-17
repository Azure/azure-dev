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

// A config can be read-only: a Perforce or TFVC checkout marks files that way
// by default, as does `attrib +R` and some archive extractions.
//
// This used to work by accident. The replacement removed the destination first,
// and os.Remove clears FILE_ATTRIBUTE_READONLY and retries the delete, so the
// attribute never reached the rename. Dropping the unlink -- which was right,
// because it opened a window where the config did not exist -- took that repair
// with it, and Windows reports a rename onto a read-only destination with the
// same errno as one a reader holds open, so it cannot be told apart earlier.
func TestSaveEvalConfigReplacesAReadOnlyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "azure.eval.yaml")
	first := &EvalConfig{Evals: []Eval{{Name: "first", EvaluationLevel: "turn"}}}
	require.NoError(t, SaveEvalConfigTo(path, first))
	require.NoError(t, os.Chmod(path, 0o400))

	second := &EvalConfig{Evals: []Eval{{Name: "second", EvaluationLevel: "turn"}}}
	require.NoError(t, SaveEvalConfigTo(path, second),
		"a read-only config has to be replaceable, as it was before the rename")

	got, err := LoadEvalConfig(path)
	require.NoError(t, err)
	require.Len(t, got.Evals, 1)
	assert.Equal(t, "second", got.Evals[0].Name, "and the new content has to be there")
}

// The retry exists for a window measured in microseconds. A file that is
// genuinely unreadable shares an errno with that window on Windows, so it pays
// the budget before it is reported -- which is only acceptable while the budget
// stays small.
func TestContentionBudgetsStaySmall(t *testing.T) {
	assert.LessOrEqual(t, readRetryBudget.Milliseconds(), int64(250),
		"every unreadable file pays this, and ReadFileNoBOM reads one per evaluator")
	assert.LessOrEqual(t, renameRetryBudget.Milliseconds(), int64(500),
		"a read-only destination waits this out before the attribute is cleared")
}

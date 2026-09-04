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

// A `$ref` on one entry does not change what a different entry means.
//
// Rubric keys at evaluator entry level were once moved under `definition` when
// the document used `$ref` anywhere. A directive on an unrelated dataset
// therefore switched that on for every evaluator in the file, and a hand-written
// `dimensions:` -- a mistake worth reporting -- was silently filed as rubric
// content and published to the service instead. A `$ref` now fills the field
// that holds it, so nothing about one entry can reach another.
//
// The same evaluator, refused in one file and accepted in another because of an
// unrelated entry, is the shape this whole mechanism is supposed to rule out.
func TestARefOnOneEntryDoesNotRescueAnother(t *testing.T) {
	withoutRef := `
datasets:
  - name: golden
    file: ./datasets/golden.jsonl
evaluators:
  - name: quality
    dimensions:
      - id: tone
        weight: 3
`
	withUnrelatedRef := `
datasets:
  - $ref: ./parts/golden.yaml
evaluators:
  - name: quality
    dimensions:
      - id: tone
        weight: 3
`

	refused := func(t *testing.T, body string) error {
		t.Helper()
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "parts"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "parts", "golden.yaml"),
			[]byte("name: golden\nfile: ./datasets/golden.jsonl\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, EvalConfigBase), []byte(body), 0o600))
		_, err := OpenEvalConfig(dir)
		return err
	}

	baseline := refused(t, withoutRef)
	require.Error(t, baseline,
		"a rubric key written at entry level is a mistake, and the strict decoder reports it")
	assert.Contains(t, baseline.Error(), "dimensions")

	withUnrelated := refused(t, withUnrelatedRef)
	require.Error(t, withUnrelated,
		"the dataset's `$ref` says nothing about this evaluator, so the same entry is still a mistake")
	assert.Contains(t, withUnrelated.Error(), "dimensions")
}

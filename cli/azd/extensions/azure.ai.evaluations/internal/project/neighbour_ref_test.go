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
// The rescue that moves spliced rubric keys under `definition` used to be gated
// on "this document uses `$ref` somewhere". A directive on an unrelated dataset
// therefore switched it on for every evaluator in the file, and a hand-written
// `dimensions:` -- a mistake the strict decoder exists to report -- was silently
// filed as rubric content and published to the service instead.
//
// The same evaluator, refused in one file and accepted in another because of a
// neighbour, is the shape this whole mechanism is supposed to rule out.
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
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "parts"), 0o755))
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

	neighbour := refused(t, withUnrelatedRef)
	require.Error(t, neighbour,
		"the dataset's `$ref` says nothing about this evaluator, so the same entry is still a mistake")
	assert.Contains(t, neighbour.Error(), "dimensions")
}

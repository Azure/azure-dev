// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The config is read by other processes while this one writes it, and
// os.WriteFile truncates before it writes. A reader landing in that window got
// zero bytes -- which parses as a VALID EMPTY CONFIG, not an error -- and would
// then write back a file with every eval missing, reporting success.
//
// Replacing by rename means a reader sees the whole old file or the whole new
// one. This drives a writer and a reader concurrently and asserts the reader
// never observes a config that lost its evals.
func TestSaveEvalConfigNeverExposesAHalfWrittenFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "azure.eval.yaml")

	full := &EvalConfig{
		Datasets: []DatasetDecl{{Name: "golden", Source: "./datasets/golden.jsonl"}},
		Evals: []Eval{
			{Name: "first", EvaluationLevel: "turn"},
			{Name: "second", EvaluationLevel: "turn"},
		},
	}
	require.NoError(t, SaveEvalConfigTo(path, full))

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var truncated int

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			cfg, err := LoadEvalConfig(path)
			if err != nil {
				continue // a read that fails is honest; a silent empty one is not
			}
			if len(cfg.Evals) != 2 {
				truncated++
			}
		}
		close(stop)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = SaveEvalConfigTo(path, full)
			}
		}
	}()
	wg.Wait()

	assert.Zerof(t, truncated,
		"a concurrent reader saw a config with its evals missing %d times", truncated)
}

// The replacement must leave the file complete and parseable.
func TestSaveEvalConfigRoundTripsThroughTheRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "azure.eval.yaml")
	want := &EvalConfig{Evals: []Eval{{Name: "only", EvaluationLevel: "turn"}}}

	require.NoError(t, SaveEvalConfigTo(path, want))
	require.NoError(t, SaveEvalConfigTo(path, want)) // over an existing file

	got, err := LoadEvalConfig(path)
	require.NoError(t, err)
	require.Len(t, got.Evals, 1)
	assert.Equal(t, "only", got.Evals[0].Name)

	// The temporary file is this function's business and must not be left over.
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "the rename must not leave a temporary file behind")
}

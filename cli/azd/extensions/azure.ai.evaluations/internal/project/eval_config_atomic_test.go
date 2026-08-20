// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
		Datasets: []DatasetDecl{{Name: "golden", File: "./datasets/golden.jsonl"}},
		Evals: []Eval{
			{Name: "first", EvaluationLevel: "turn"},
			{Name: "second", EvaluationLevel: "turn"},
		},
	}
	require.NoError(t, SaveEvalConfigTo(path, full))

	// Every field, not the eval count. A document caught mid-write can still
	// parse with two evals while having lost the datasets, or a field off the
	// second one, and counting entries reports that as a whole file. Read back
	// what a correct read returns and hold every later read to it.
	baseline, err := LoadEvalConfig(path)
	require.NoError(t, err)
	require.Len(t, baseline.Evals, 2)
	require.Len(t, baseline.Datasets, 1)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	// Counted apart, because they want opposite responses. A config observed
	// with fields missing is the bug this test guards. A read that failed under
	// contention may only mean the retry budget was short on a loaded machine.
	// One combined counter, or one example off whichever happened first, leaves
	// a run where both occurred looking like whichever won the race.
	var readErrors, mismatches int
	var firstReadError, firstMismatch string
	var replacements int64

	wg.Go(func() {
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			cfg, err := LoadEvalConfig(path)
			if err != nil {
				// NOT skipped: "the file does not exist" is exactly what a
				// remove-then-rename exposes, and OpenEvalConfig turns it into
				// "there is no configuration yet" -- the same loss this guards
				// against, by another route.
				readErrors++
				if firstReadError == "" {
					firstReadError = err.Error()
				}
				continue
			}
			if !reflect.DeepEqual(cfg, baseline) {
				mismatches++
				if firstMismatch == "" {
					firstMismatch = fmt.Sprintf(
						"%d evals and %d datasets, wanted %d and %d",
						len(cfg.Evals), len(cfg.Datasets),
						len(baseline.Evals), len(baseline.Datasets))
				}
			}
		}
		close(stop)
	})

	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				if SaveEvalConfigTo(path, full) == nil {
					atomic.AddInt64(&replacements, 1)
				}
			}
		}
	})
	wg.Wait()

	require.NotZero(t, atomic.LoadInt64(&replacements),
		"the writer has to have replaced the file, or nothing was under test")
	assert.Zerof(t, readErrors+mismatches,
		"over %d replacements a concurrent reader saw %d incomplete configs (first: %s) "+
			"and %d failed reads (first: %s)",
		atomic.LoadInt64(&replacements),
		mismatches, orNone(firstMismatch),
		readErrors, orNone(firstReadError))
}

// orNone keeps an absent example from reading as an empty one.
func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// OpenEvalConfig maps a missing file to "no configuration yet", which callers
// answer by writing a fresh one. So a replacement that momentarily unlinks the
// destination is as destructive as one that truncates it, and this pins the
// window closed from that side too.
func TestOpenEvalConfigNeverSeesTheFileVanish(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "azure.eval.yaml")
	full := &EvalConfig{Evals: []Eval{{Name: "first", EvaluationLevel: "turn"}}}
	require.NoError(t, SaveEvalConfigTo(path, full))

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var vanished, replacements int64

	wg.Go(func() {
		// Wall clock, not an iteration count. Three hundred os.Stat calls take
		// microseconds, which is not long enough for the writer to be scheduled
		// even once -- the test passed against the unlinking version it was
		// written to catch.
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				atomic.AddInt64(&vanished, 1)
			}
		}
		close(stop)
	})
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				if SaveEvalConfigTo(path, full) == nil {
					atomic.AddInt64(&replacements, 1)
				}
			}
		}
	})
	wg.Wait()

	require.NotZero(t, atomic.LoadInt64(&replacements),
		"the writer has to have replaced the file, or nothing was under test")
	assert.Zerof(t, vanished, "the config was absent %d times during replacement", vanished)
}

// The replacement must leave the file complete and parseable.
func TestSaveEvalConfigRoundTripsThroughTheRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "azure.eval.yaml")
	want := &EvalConfig{Evals: []Eval{{Name: "only", EvaluationLevel: "turn"}}}

	// A different first write, so the second one has something to replace.
	// Writing the same payload twice passes even if the second save silently
	// left the original file where it was, which is the case worth catching.
	first := &EvalConfig{Evals: []Eval{{Name: "replaced", EvaluationLevel: "conversation"}}}
	require.NoError(t, SaveEvalConfigTo(path, first))
	require.NoError(t, SaveEvalConfigTo(path, want))

	got, err := LoadEvalConfig(path)
	require.NoError(t, err)
	require.Len(t, got.Evals, 1)
	assert.Equal(t, "only", got.Evals[0].Name, "the second save has to have replaced the first")
	assert.Equal(t, "turn", got.Evals[0].EvaluationLevel)

	// The temporary file is this function's business and must not be left over.
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "the rename must not leave a temporary file behind")
}

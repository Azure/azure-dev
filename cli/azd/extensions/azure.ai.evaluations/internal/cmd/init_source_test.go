// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSettleInitSource covers the flag rules and the defaulting together,
// because holding them apart is what broke.
//
// The row that matters is `--max-traces where traces is the default`:
// `azd ai eval init --max-traces 50` in a project wired for traces. The guard
// used to run on the flag as typed, so it saw an empty source, decided that was
// not "traces", and refused -- one line before the defaulting was going to
// choose traces anyway.
func TestSettleInitSource(t *testing.T) {
	const (
		wired    = true
		notWired = false
		given    = true
		notGiven = false
	)

	cases := []struct {
		name       string
		explicit   string
		maxTraces  bool
		traceDays  bool
		traces     bool
		datasets   int
		wantSource string
		wantErr    bool
	}{
		{"explicit traces stands", initSourceTraces, notGiven, notGiven, notWired, 0, initSourceTraces, false},
		{"explicit dataset stands", initSourceDataset, notGiven, notGiven, wired, 0, initSourceDataset, false},
		{"no source, no traces wired", "", notGiven, notGiven, notWired, 0, initSourceDataset, false},
		{"no source, traces wired", "", notGiven, notGiven, wired, 0, initSourceTraces, false},

		// Telemetry outranks a declared dataset: a project collecting traces
		// has real conversations, and a declaration is only a reference.
		{"traces win over a declared dataset", "", notGiven, notGiven, wired, 1, initSourceTraces, false},
		{"one declared dataset and no traces", "", notGiven, notGiven, notWired, 1, initSourceDataset, false},

		{"--max-traces with explicit traces", initSourceTraces, given, notGiven, notWired, 0, initSourceTraces, false},
		{"--max-traces with explicit dataset is refused", initSourceDataset, given, notGiven, wired, 0, "", true},
		{"--max-traces where dataset is the default is refused", "", given, notGiven, notWired, 0, "", true},

		// The regression.
		{"--max-traces where traces is the default", "", given, notGiven, wired, 0, initSourceTraces, false},

		// --trace-days bounds the same rows, so it is refused the same way --
		// and, for the same reason, only after the source is known.
		{"--trace-days with explicit traces", initSourceTraces, notGiven, given, notWired, 0, initSourceTraces, false},
		{"--trace-days with explicit dataset is refused", initSourceDataset, notGiven, given, wired, 0, "", true},
		{"--trace-days where dataset is the default is refused", "", notGiven, given, notWired, 0, "", true},
		{"--trace-days where traces is the default", "", notGiven, given, wired, 0, initSourceTraces, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := settleInitSource(noPromptCmd(t, true), initSourceInput{
				explicit:       tc.explicit,
				maxTracesGiven: tc.maxTraces,
				traceDaysGiven: tc.traceDays,
				usableDatasets: tc.datasets,
				tracesWired:    func() bool { return tc.traces },
			})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("wanted a refusal, got source %q", got)
				}
				if !strings.Contains(err.Error(), "traces") {
					t.Errorf("the refusal never says what the flag needs: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if got != tc.wantSource {
				t.Errorf("source = %q, want %q", got, tc.wantSource)
			}
		})
	}
}

// TestSettleInitSourceDoesNotAskWhenItWasTold pins the connection cost. Every
// call to tracesWired opens an azd connection, and a command given its source
// has no question to ask.
func TestSettleInitSourceDoesNotAskWhenItWasTold(t *testing.T) {
	asked := 0
	probe := func() bool { asked++; return true }

	for _, source := range []string{initSourceDataset, initSourceTraces} {
		_, err := settleInitSource(noPromptCmd(t, true), initSourceInput{
			explicit:    source,
			tracesWired: probe,
		})
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	if asked != 0 {
		t.Errorf("opened %d connection(s) to answer a question that was not asked", asked)
	}
}

// A declaration whose file is gone is not a reason to default to a dataset
// source: it is the one dataset the scaffold could not have run against.
func TestUsableDatasetCountSkipsMissingFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "here.jsonl"), []byte("{}\n"), 0o600))

	cfg := &project.EvalConfig{Datasets: []project.DatasetDecl{
		{Name: "registered"},
		{Name: "here", File: "here.jsonl"},
		{Name: "gone", File: "gone.jsonl"},
	}}

	assert.Equal(t, 2, usableDatasetCount(cfg, dir),
		"a registered reference counts unverified, and a present file counts; a missing one does not")
}

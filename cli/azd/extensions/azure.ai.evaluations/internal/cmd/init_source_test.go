// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"
)

// TestSettleInitSource covers the flag rule and the defaulting together,
// because holding them apart is what broke.
//
// The row that matters is the last one: `azd ai eval init --max-traces 50` in a
// project wired for traces. The guard used to run on the flag as typed, so it
// saw an empty source, decided that was not "traces", and refused -- one line
// before the defaulting was going to choose traces anyway.
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
		traces     bool
		wantSource string
		wantErr    bool
	}{
		{"explicit traces stands", initSourceTraces, notGiven, notWired, initSourceTraces, false},
		{"explicit dataset stands", initSourceDataset, notGiven, wired, initSourceDataset, false},
		{"no source, no traces wired", "", notGiven, notWired, initSourceDataset, false},
		{"no source, traces wired", "", notGiven, wired, initSourceTraces, false},

		{"--max-traces with explicit traces", initSourceTraces, given, notWired, initSourceTraces, false},
		{"--max-traces with explicit dataset is refused", initSourceDataset, given, wired, "", true},
		{"--max-traces where dataset is the default is refused", "", given, notWired, "", true},

		// The regression.
		{"--max-traces where traces is the default", "", given, wired, initSourceTraces, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := settleInitSource(tc.explicit, tc.maxTraces, func() bool {
				return tc.traces
			})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("wanted a refusal, got source %q", got)
				}
				if !strings.Contains(err.Error(), "traces") {
					t.Errorf("the refusal never says what --max-traces needs: %v", err)
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
		if _, err := settleInitSource(source, false, probe); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	if asked != 0 {
		t.Errorf("opened %d connection(s) to answer a question that was not asked", asked)
	}
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"testing"

	"github.com/fatih/color"
)

// TestMain pins color off for the whole package, and stops a JSON failure from
// taking the test binary with it.
//
// fatih/color decides once, at init, from whether the process's stdout is a
// terminal -- not from the writer a renderer was handed. `go test` pipes
// stdout, so a test asserting on plain text passes under `go test` and fails
// when the compiled test binary is run from a terminal. Pinning it here makes
// the expected output the same either way, rather than leaving every assertion
// on a rendered line to depend on how the suite was started.
//
// failAs ends the process after answering a JSON caller, because azd writes the
// error it is handed to stdout and would otherwise append prose to the document.
// That only matters when azd is the one running the binary, so it is pinned by
// the smoke test against a packed build rather than in process here, and the
// exit is disarmed for every test in the package -- swapping it per test would
// race with the parallel ones.
func TestMain(m *testing.M) {
	color.NoColor = true
	exitProcess = func(int) {}
	os.Exit(m.Run())
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"testing"
)

// TestMain stops a JSON failure from taking the test binary with it.
//
// failAs ends the process after answering a JSON caller, because azd writes the
// error it is handed to stdout and would otherwise append prose to the document.
// That only matters when azd is the one running the binary, so it is pinned by
// the smoke test against a packed build rather than in process here, and the
// exit is disarmed for every test in the package -- swapping it per test would
// race with the parallel ones.
func TestMain(m *testing.M) {
	exitProcess = func(int) {}
	os.Exit(m.Run())
}

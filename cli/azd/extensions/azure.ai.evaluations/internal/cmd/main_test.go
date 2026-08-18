// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"testing"

	"github.com/fatih/color"
)

// TestMain pins colour off for the whole package.
//
// fatih/color decides once, at init, from whether the process's stdout is a
// terminal -- not from the writer a renderer was handed. `go test` pipes
// stdout, so a test asserting on plain text passes under `go test` and fails
// when the compiled test binary is run from a terminal. Pinning it here makes
// the expected output the same either way, rather than leaving every assertion
// on a rendered line to depend on how the suite was started.
func TestMain(m *testing.M) {
	color.NoColor = true
	os.Exit(m.Run())
}

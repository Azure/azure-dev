// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The name is the caller's and the version is the service's, and both were
// interpolated straight into a destination path. filepath.Join cleans a `..`
// rather than refusing it, so `--version ../..` resolved outside the directory
// the download was pointed at.
func TestADerivedDestinationCannotLeaveTheOutputDirectory(t *testing.T) {
	escapes := []struct{ name, version string }{
		{"golden", "../../etc/passwd"},
		{"../../golden", "1"},
		{"golden", ".."},
		{"golden", "a/b"},
		{"golden", `a\b`},
		{"golden", ""},
	}

	for _, c := range escapes {
		t.Run(c.name+"|"+c.version, func(t *testing.T) {
			_, err := derivedLeafName(c.name, c.version, ".jsonl")

			require.Errorf(t, err, "%q/%q must not become a path", c.name, c.version)
			assert.Contains(t, err.Error(), "--output-dir",
				"the refusal names the flag that does let them choose a destination")
		})
	}
}

// An ordinary name and version still derive the destination they always did.
func TestADerivedDestinationKeepsItsOrdinaryShape(t *testing.T) {
	leaf, err := derivedLeafName("golden", "2.0", ".jsonl")

	require.NoError(t, err)
	assert.Equal(t, "golden-2.0.jsonl", leaf)
	assert.Equal(t, filepath.Join("out", "golden-2.0.jsonl"), filepath.Join("out", leaf))
}

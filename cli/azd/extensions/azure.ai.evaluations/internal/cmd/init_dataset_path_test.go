// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A dataset inside the eval directory is reached without climbing out of it.
//
// The location is the directory before anything is written and the
// configuration file once it exists, which is what a second `init` resolves to.
// Rebasing against the file put a `..` in front of every path, so
// `./evals/datasets/rows.jsonl` was written as `../datasets/rows.jsonl` and the
// deploy looked for it beside the project rather than beside the config.
func TestADatasetInsideTheEvalDirNeedsNoDotDot(t *testing.T) {
	for _, tc := range []struct {
		name         string
		location     string
		configOnDisk bool
	}{
		{"first init, the location is the directory", "evals", false},
		{"second init, the location is the file", filepath.Join("evals", "azure.eval.yaml"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			require.NoError(t, os.MkdirAll(filepath.Join("evals", "datasets"), 0o750))
			require.NoError(t, os.WriteFile(
				filepath.Join("evals", "datasets", "rows.jsonl"), []byte("{}\n"), 0o600))
			if tc.configOnDisk {
				require.NoError(t, os.WriteFile(
					filepath.Join("evals", "azure.eval.yaml"), []byte("evals: []\n"), 0o600))
			}

			_, cfg := scaffoldFor(t, scaffoldInput{
				evalName: "smoke",
				target:   "a",
				dataset:  filepath.Join("evals", "datasets", "rows.jsonl"),
				evalDir:  tc.location,
			})

			decl, ok := cfg.DatasetDeclaration("rows")
			require.True(t, ok)
			assert.Equal(t, "./datasets/rows.jsonl", decl.File,
				"the rows sit beside the configuration")
		})
	}
}

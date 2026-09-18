// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The shipped examples are copied, so a wrong key in one is a wrong key in
// whatever the reader builds from it.
//
// inline.azure.yaml wrote a rubric dimension as `name:`, but the service field
// is `id` -- the example unmarshalled with an empty dimension ID and failed on
// deploy. The schema cannot catch this: it deliberately leaves the keys under
// `definition` to the service, so nothing was checking the example at all.
func TestExamplesUseTheServiceSpellingForRubricDimensions(t *testing.T) {
	root, err := os.OpenRoot("../..")
	require.NoError(t, err)
	defer func() { _ = root.Close() }()

	entries, err := root.FS().(interface {
		ReadDir(string) ([]os.DirEntry, error)
	}).ReadDir("schemas/examples")
	require.NoError(t, err)

	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		f, err := root.Open("schemas/examples/" + e.Name())
		require.NoError(t, err)
		body, err := io.ReadAll(f)
		_ = f.Close()
		require.NoError(t, err)

		lines := strings.Split(string(body), "\n")
		for i, line := range lines {
			if strings.TrimSpace(line) != "dimensions:" {
				continue
			}
			checked++
			// The first entry of the list says which spelling the example uses.
			for _, next := range lines[i+1:] {
				trimmed := strings.TrimSpace(next)
				if trimmed == "" || strings.HasPrefix(trimmed, "#") {
					continue
				}
				assert.Truef(t, strings.HasPrefix(trimmed, "- id:"),
					"%s writes a rubric dimension as %q; the service field is `id`",
					e.Name(), trimmed)
				break
			}
		}
	}
	assert.Positive(t, checked, "no example declares a rubric, so this checks nothing")
}

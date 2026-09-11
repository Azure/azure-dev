// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Cursor listings are walked in exactly one place.
//
// `collectPages` was taught to return an error when a walk cannot finish, so a
// caller cannot mistake a truncated catalog for a complete one. ListOutputItems
// carried a second copy of the same loop and kept handing back a partial list
// as success long afterwards -- the rows that drive `run output list`, the mean
// scores and every export.
//
// A third copy is how that returns, and it returns quietly: the call succeeds,
// the rows are simply fewer than the run. So the shape is worth failing the
// build over.
func TestCursorListingsAreWalkedInOnePlace(t *testing.T) {
	const walker = "collectPages"

	// The tell is a cursor-repeat guard written inline: a `seen` set keyed by
	// the cursor the service last returned.
	sightings := map[string][]int{}
	root, err := os.OpenRoot(".")
	require.NoError(t, err)
	defer func() { _ = root.Close() }()
	fsys := root.FS()

	require.NoError(t, fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil // including this file, which spells out the shape it looks for
		}
		body, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		insideWalker := false
		for i, line := range strings.Split(string(body), "\n") {
			// The walker itself is the one place allowed to say this.
			if strings.Contains(line, "func "+walker+"(") {
				insideWalker = true
				continue
			}
			if insideWalker {
				if strings.HasPrefix(line, "}") {
					insideWalker = false
				}
				continue
			}
			if strings.Contains(line, "seen[") && strings.Contains(line, "LastID") {
				sightings[path] = append(sightings[path], i+1)
			}
		}
		return nil
	}))

	assert.Empty(t, sightings,
		"a cursor walk is re-implemented at %v; route it through %s instead, or the "+
			"next rule about finishing a listing will land on one caller and not the other",
		sightings, walker)
}

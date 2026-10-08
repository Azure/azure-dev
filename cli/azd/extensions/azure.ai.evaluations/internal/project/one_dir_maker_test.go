// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The eval directory is created in exactly one place.
//
// Callers hold a location, not a directory: it is the directory before anything
// is written and the configuration file once it exists, and a second `init`
// reads the recorded path back and gets the file. Four functions created it
// with a bare os.MkdirAll, and all four then asked for a directory named
// azure.eval.yaml.
//
// Fixing one of them looked like fixing the bug -- the unit test passed, and
// the command still failed, because the flow went through a second copy. So
// the shape is worth failing the build over.
func TestTheEvalDirectoryIsCreatedInOnePlace(t *testing.T) {
	const helper = "ensureEvalDir"

	root, err := os.OpenRoot(".")
	require.NoError(t, err)
	defer func() { _ = root.Close() }()
	fsys := root.FS()

	var offenders []string
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
		for i, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.Contains(trimmed, "os.MkdirAll(") {
				continue
			}
			// Creating a subdirectory of an already-resolved directory is fine;
			// creating the location itself is what has to go through the helper.
			if strings.Contains(trimmed, "os.MkdirAll(evalDir") ||
				strings.Contains(trimmed, "os.MkdirAll(location") {
				offenders = append(offenders,
					path+":"+strconv.Itoa(i+1)+": "+trimmed)
			}
		}
		return nil
	}))

	assert.Empty(t, offenders,
		"create the eval directory with %s, which resolves a location that may be "+
			"the configuration file rather than the directory holding it", helper)
}

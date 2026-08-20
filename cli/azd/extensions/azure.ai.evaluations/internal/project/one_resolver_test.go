// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `$ref` is resolved in exactly one place.
//
// The CLI and `azd up` reach a configuration by different routes and each used
// to resolve for itself, so every rule had to be added twice -- and twice it
// was not, each time producing an include one route accepted and the other
// refused. A second caller of ResolveFileRefs is how that comes back, and it
// comes back silently, so it is worth failing the build over.
func TestRefsAreResolvedInOnePlace(t *testing.T) {
	const resolver = "resolveEvalRefs"

	callers := map[string][]int{}
	require.NoError(t, filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil // including this file, which names the call it is counting
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(body), "\n") {
			if strings.Contains(line, "foundry.ResolveFileRefs(") {
				callers[path] = append(callers[path], i+1)
			}
		}
		return nil
	}))

	total := 0
	for _, lines := range callers {
		total += len(lines)
	}
	assert.Equal(t, 1, total,
		"ResolveFileRefs has more than one caller (%v); route it through %s instead, "+
			"or the next `$ref` rule will land on one path and not the other",
		callers, resolver)
}

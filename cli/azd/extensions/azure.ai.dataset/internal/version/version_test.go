// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package version

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The build scripts set these variables with the linker's -X flag, and -X is
// discarded without complaint when the symbol it names does not exist. These
// scripts began as copies of the eval extension's and kept its module path, so
// every release binary reported "dev" in its User-Agent and no service-side log
// could tell which build a caller was running. Nothing failed; it just never
// worked.
//
// Comparing against go.mod rather than a literal keeps this honest if the
// module is ever renamed.
func TestBuildScriptsStampThisModule(t *testing.T) {
	root := filepath.Join("..", "..")

	// #nosec G304 -- root is the module directory this test computed, not caller input.
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	require.NoError(t, err)

	var module string
	for line := range strings.SplitSeq(string(goMod), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			module = strings.TrimSpace(rest)
			break
		}
	}
	require.NotEmpty(t, module, "go.mod has to declare a module")

	want := module + "/internal/version"
	for _, script := range []string{"build.ps1", "build.sh", "ci-build.ps1"} {
		// #nosec G304 -- script comes from this test's own table of repo paths.
		body, err := os.ReadFile(filepath.Join(root, script))
		require.NoError(t, err)

		text := string(body)
		require.Contains(t, text, want,
			"%s stamps a module path that is not this one, so -X is silently dropped", script)

		for _, sibling := range []string{"azureaieval/internal/version"} {
			require.NotContains(t, text, sibling,
				"%s still names a sibling extension's version package", script)
		}
	}
}

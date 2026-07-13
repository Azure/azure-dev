// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGitignoreEmbedded verifies that the dotfiles (.gitignore) shipped with each
// language template are embedded. Without the `all:` prefix on the go:embed
// directive these files are silently skipped, which previously meant generated
// extensions had no .gitignore (so build artifacts under bin/ could be committed).
func TestGitignoreEmbedded(t *testing.T) {
	for _, language := range []string{"go", "dotnet", "javascript", "python"} {
		t.Run(language, func(t *testing.T) {
			contents, err := Languages.ReadFile("languages/" + language + "/.gitignore")
			require.NoError(t, err)
			require.NotEmpty(t, contents)
		})
	}
}

// TestGoGitignoreExcludesBin ensures the generated Go extension ignores the build
// output directory so binaries are not accidentally committed.
func TestGoGitignoreExcludesBin(t *testing.T) {
	contents, err := Languages.ReadFile("languages/go/.gitignore")
	require.NoError(t, err)
	require.Contains(t, string(contents), "bin/")
}

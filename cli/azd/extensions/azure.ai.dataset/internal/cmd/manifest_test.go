// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The build stamps the binary from version.txt while the registry publishes
// what extension.yaml says, so a drift ships a binary that misreports its own
// version. Bumping one and forgetting the other is the easy mistake, and it
// happened here: extension.yaml went to beta.4 while version.txt stayed on
// beta.3. The sibling eval extension already had this check, which is how the
// drift was noticed there; this extension had nothing and stayed quiet.
//
// The version line is read rather than parsed as YAML: this module has no
// direct YAML dependency, and a test is a poor reason to add one.
func TestManifestVersionMatchesVersionFile(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "extension.yaml"))
	require.NoError(t, err, "reading extension.yaml")

	var declared string
	for line := range strings.SplitSeq(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "version:"); ok {
			declared = strings.TrimSpace(rest)
			break
		}
	}
	require.NotEmpty(t, declared, "extension.yaml must declare a version")

	stamped, err := os.ReadFile(filepath.Join("..", "..", "version.txt"))
	require.NoError(t, err, "reading version.txt")

	require.Equal(t, strings.TrimSpace(string(stamped)), declared,
		"version.txt and extension.yaml must agree")
}

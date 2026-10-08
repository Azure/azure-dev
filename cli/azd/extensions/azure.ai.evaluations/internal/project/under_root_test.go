// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rule the deploy path learned and the CLI paths now share: a project
// relative path means the same file whatever directory the process is standing
// in, and the only base that makes that true is the one holding azure.yaml.
func TestUnderRootResolvesAProjectRelativePath(t *testing.T) {
	root := t.TempDir()

	assert.Equal(t, filepath.Join(root, "evals"), UnderRoot(root, "evals"))
	assert.Equal(t, filepath.Join(root, "config", "nightly.yaml"),
		UnderRoot(root, filepath.Join("config", "nightly.yaml")))
}

// azd does not re-root an absolute `$ref`, so neither does this: joining one
// under the project produced <root>/C:/shared/evals.
func TestUnderRootLeavesAnAbsolutePathAlone(t *testing.T) {
	absolute := filepath.Join(t.TempDir(), "shared", "evals")
	require.True(t, filepath.IsAbs(absolute), "the fixture has to be absolute to test this")

	assert.Equal(t, absolute, UnderRoot(t.TempDir(), absolute))
}

// An empty root is azd having failed to name the project. Joining onto nothing
// would silently re-root every relative path at the filesystem root.
func TestUnderRootWithoutARootKeepsThePathAsGiven(t *testing.T) {
	assert.Equal(t, "evals", UnderRoot("", "evals"))
	assert.Equal(t, filepath.Join("config", "nightly.yaml"),
		UnderRoot("", filepath.Join("config", "nightly.yaml")))
}

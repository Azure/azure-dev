// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

// TestDetermineArtifactKind locks --from-package classification, including the
// compound archive suffixes that filepath.Ext cannot recognize.
func TestDetermineArtifactKind(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	archiveNames := []string{
		"pkg.zip", "pkg.tar", "pkg.tgz", "pkg.txz", "pkg.tbz2",
		"pkg.tar.gz", "pkg.tar.bz2", "pkg.tar.xz",
		"PKG.TAR.GZ", // classification is case-insensitive
	}
	for _, name := range archiveNames {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
		require.Equalf(t, project.ArtifactKindArchive, determineArtifactKind(p),
			"expected archive for %q", name)
	}

	// An existing directory is a directory artifact.
	require.Equal(t, project.ArtifactKindDirectory, determineArtifactKind(dir))

	// A path that does not exist is treated as a container image reference.
	require.Equal(t, project.ArtifactKindContainer,
		determineArtifactKind("myregistry.azurecr.io/app:latest"))

	// An existing non-archive file is also treated as a container reference.
	other := filepath.Join(dir, "notanarchive.bin")
	require.NoError(t, os.WriteFile(other, []byte("x"), 0o600))
	require.Equal(t, project.ArtifactKindContainer, determineArtifactKind(other))
}

// TestFromPackageMetadataConstant keeps the core alias and the SDK constant in
// sync so core and extensions agree on the provenance marker.
func TestFromPackageMetadataConstant(t *testing.T) {
	t.Parallel()
	require.Equal(t, "azd.fromPackage", project.MetadataKeyFromPackage)
	require.Equal(t, azdext.ArtifactMetadataKeyFromPackage, project.MetadataKeyFromPackage)
}

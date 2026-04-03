// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_appServiceTarget_Package_Coverage3(t *testing.T) {
	t.Run("Success_CreatesZip", func(t *testing.T) {
		tmpDir := t.TempDir()
		pkgDir := filepath.Join(tmpDir, "pkg")
		require.NoError(t, os.MkdirAll(pkgDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "app.py"), []byte("print('hi')"), 0o600))

		sc := &ServiceConfig{
			Name:     "web",
			Language: ServiceLanguagePython,
			Project:  &ProjectConfig{Path: tmpDir},
		}

		sctx := NewServiceContext()
		require.NoError(t, sctx.Package.Add(&Artifact{
			Kind:         ArtifactKindDirectory,
			Location:     pkgDir,
			LocationKind: LocationKindLocal,
		}))

		st := &appServiceTarget{}
		progress := async.NewProgress[ServiceProgress]()
		go func() {
			for range progress.Progress() {
			}
		}()

		result, err := st.Package(t.Context(), sc, sctx, progress)
		progress.Done()

		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotEmpty(t, result.Artifacts)

		zipArtifact, found := result.Artifacts.FindFirst(WithKind(ArtifactKindArchive))
		require.True(t, found)
		assert.FileExists(t, zipArtifact.Location)
		assert.Equal(t, pkgDir, zipArtifact.Metadata["packagePath"])
	})

	t.Run("NoArtifact_Error", func(t *testing.T) {
		sc := &ServiceConfig{
			Name:     "web",
			Language: ServiceLanguagePython,
			Project:  &ProjectConfig{Path: t.TempDir()},
		}

		sctx := NewServiceContext()
		st := &appServiceTarget{}
		progress := async.NewProgress[ServiceProgress]()
		go func() {
			for range progress.Progress() {
			}
		}()

		_, err := st.Package(t.Context(), sc, sctx, progress)
		progress.Done()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no package artifacts found")
	})
}

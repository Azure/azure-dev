package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_HasAppHost_Extended_Coverage3(t *testing.T) {
	t.Run("DotNetService_CanImportTrue", func(t *testing.T) {
		tempDir := t.TempDir()
		dotNetPath := filepath.Join(tempDir, "apphost")
		os.MkdirAll(dotNetPath, 0755)

		importer := NewDotNetImporter(nil, nil, nil, nil, nil)
		// Pre-populate the hostCheck cache so CanImport returns true without needing a real CLI
		importer.hostCheck[dotNetPath] = hostCheckResult{is: true}

		im := NewImportManager(importer)
		prj := &ProjectConfig{
			Path: tempDir,
			Services: map[string]*ServiceConfig{
				"apphost": {
					Name:         "apphost",
					Language:     ServiceLanguageDotNet,
					RelativePath: "apphost",
					Project:      &ProjectConfig{Path: tempDir},
				},
			},
		}
		result := im.HasAppHost(context.Background(), prj)
		assert.True(t, result)
	})

	t.Run("DotNetService_CanImportError", func(t *testing.T) {
		tempDir := t.TempDir()
		importer := NewDotNetImporter(nil, nil, nil, nil, nil)
		importer.hostCheck[tempDir] = hostCheckResult{is: false, err: errors.New("detection failed")}

		im := NewImportManager(importer)
		prj := &ProjectConfig{
			Path: tempDir,
			Services: map[string]*ServiceConfig{
				"apphost": {
					Name:         "apphost",
					Language:     ServiceLanguageDotNet,
					RelativePath: ".",
					Project:      &ProjectConfig{Path: tempDir},
				},
			},
		}
		// Should return false and log the error
		result := im.HasAppHost(context.Background(), prj)
		assert.False(t, result)
	})

	t.Run("DotNetService_CanImportFalse", func(t *testing.T) {
		tempDir := t.TempDir()
		importer := NewDotNetImporter(nil, nil, nil, nil, nil)
		importer.hostCheck[tempDir] = hostCheckResult{is: false}

		im := NewImportManager(importer)
		prj := &ProjectConfig{
			Path: tempDir,
			Services: map[string]*ServiceConfig{
				"apphost": {
					Name:         "apphost",
					Language:     ServiceLanguageDotNet,
					RelativePath: ".",
					Project:      &ProjectConfig{Path: tempDir},
				},
			},
		}
		result := im.HasAppHost(context.Background(), prj)
		assert.False(t, result)
	})
}

func Test_functionAppTarget_Package_Coverage3(t *testing.T) {
	t.Run("WithDirectoryArtifact_CreatesZip", func(t *testing.T) {
		tempDir := t.TempDir()
		// Create a file in the temp dir for the zip to contain
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, "index.js"), []byte("exports.handler = () => {}"), 0600))

		target := &functionAppTarget{}
		svcConfig := &ServiceConfig{
			Name:     "func-svc",
			Language: ServiceLanguageJavaScript,
			Project:  &ProjectConfig{},
		}

		svcCtx := NewServiceContext()
		require.NoError(t, svcCtx.Package.Add(
			&Artifact{Kind: ArtifactKindDirectory, Location: tempDir, LocationKind: LocationKindLocal},
		))

		progress := async.NewProgress[ServiceProgress]()
		// Drain progress channel to prevent blocking
		go func() {
			for range progress.Progress() {
			}
		}()

		result, err := target.Package(context.Background(), svcConfig, svcCtx, progress)
		progress.Done()
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Artifacts, 1)
		assert.Equal(t, ArtifactKindArchive, result.Artifacts[0].Kind)
		assert.Equal(t, LocationKindLocal, result.Artifacts[0].LocationKind)
		// Should end in .zip
		assert.Equal(t, ".zip", filepath.Ext(result.Artifacts[0].Location))
	})

	t.Run("WithZipArtifact_PassThrough", func(t *testing.T) {
		tempDir := t.TempDir()
		zipPath := filepath.Join(tempDir, "deploy.zip")
		require.NoError(t, os.WriteFile(zipPath, []byte("fake-zip"), 0600))

		target := &functionAppTarget{}
		svcCtx := NewServiceContext()
		require.NoError(t, svcCtx.Package.Add(
			&Artifact{Kind: ArtifactKindDirectory, Location: zipPath, LocationKind: LocationKindLocal},
		))

		progress := async.NewProgress[ServiceProgress]()
		go func() {
			for range progress.Progress() {
			}
		}()

		result, err := target.Package(context.Background(), nil, svcCtx, progress)
		progress.Done()
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Artifacts, 1)
		assert.Equal(t, zipPath, result.Artifacts[0].Location)
	})

	t.Run("NoArtifact_Error", func(t *testing.T) {
		target := &functionAppTarget{}
		svcCtx := NewServiceContext()
		progress := async.NewProgress[ServiceProgress]()
		go func() {
			for range progress.Progress() {
			}
		}()

		_, err := target.Package(context.Background(), nil, svcCtx, progress)
		progress.Done()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no build result")
	})
}

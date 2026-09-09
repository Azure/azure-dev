// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTemplateCacheRoundTrip(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	t.Setenv(templateCacheDirEnv, cacheDir)
	pointer := "https://github.com/example/samples/blob/main/basic/azure.yaml"

	downloaded := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(downloaded, "azure.yaml"),
		[]byte("name: cached-sample\nservices: {}\n"),
		0600,
	))
	require.NoError(t, os.MkdirAll(filepath.Join(downloaded, "src"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(downloaded, "src", "main.py"), []byte("print('ok')\n"), 0600))

	require.NoError(t, refreshTemplateCache(pointer, downloaded))
	require.FileExists(t, filepath.Join(cacheDir, templateCacheRefreshedMarker))
	otherPointer := "https://github.com/example/samples/blob/main/other/azure.yaml"
	require.NotEqual(t, templateCacheDir(pointer), templateCacheDir(otherPointer))
	_, ok := readCachedTemplateManifest(otherPointer)
	require.False(t, ok)

	content, ok := readCachedTemplateManifest(pointer)
	require.True(t, ok)
	require.Contains(t, string(content), "cached-sample")

	staging := t.TempDir()
	restored, err := restoreCachedTemplate(pointer, staging)
	require.NoError(t, err)
	require.True(t, restored)
	require.True(t, fileExists(filepath.Join(staging, "src", "main.py")))
}

func TestUseCachedTemplateOnDownloadError(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	t.Setenv(templateCacheDirEnv, cacheDir)
	pointer := "https://github.com/example/samples/blob/main/basic/azure.yaml"
	downloadErr := errors.New("GitHub unavailable")

	t.Run("cache hit", func(t *testing.T) {
		downloaded := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(downloaded, "azure.yaml"), []byte("name: cached\n"), 0600))
		require.NoError(t, refreshTemplateCache(pointer, downloaded))

		staging := t.TempDir()
		require.NoError(t, useCachedTemplateOnDownloadError(pointer, staging, downloadErr))
		require.FileExists(t, filepath.Join(staging, "azure.yaml"))
	})

	t.Run("cache miss", func(t *testing.T) {
		missingPointer := "https://github.com/example/samples/blob/main/missing/azure.yaml"
		err := useCachedTemplateOnDownloadError(missingPointer, t.TempDir(), downloadErr)
		require.ErrorIs(t, err, downloadErr)
	})
}

func TestRefreshTemplateCachePreservesPreviousCacheOnActivationFailure(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	t.Setenv(templateCacheDirEnv, cacheDir)
	pointer := "https://github.com/example/samples/blob/main/basic/azure.yaml"

	previous := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(previous, "azure.yaml"), []byte("name: previous\n"), 0600))
	require.NoError(t, refreshTemplateCache(pointer, previous))

	originalRename := renameTemplateCachePath
	renameTemplateCachePath = func(oldPath, newPath string) error {
		if oldPath == templateCacheDir(pointer)+".new" {
			return errors.New("simulated activation failure")
		}
		return os.Rename(oldPath, newPath)
	}
	t.Cleanup(func() { renameTemplateCachePath = originalRename })

	replacement := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(replacement, "azure.yaml"), []byte("name: replacement\n"), 0600))
	require.Error(t, refreshTemplateCache(pointer, replacement))

	content, ok := readCachedTemplateManifest(pointer)
	require.True(t, ok)
	require.Contains(t, string(content), "previous")
}

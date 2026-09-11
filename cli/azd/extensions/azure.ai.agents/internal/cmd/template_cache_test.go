// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTemplateCacheWarningFile(t *testing.T) {
	for _, tc := range []struct {
		name        string
		tfBuild     string
		cacheHit    bool
		downloadErr error
		wantWarning bool
	}{
		{"fallback", "True", true, errors.New("GitHub 503: 50%\r\nretry]"), true},
		{"cache miss", "True", false, errors.New("GitHub unavailable"), false},
		{"canceled", "True", true, context.Canceled, false},
		{"deadline", "True", true, context.DeadlineExceeded, false},
		{"outside ADO", "", true, errors.New("GitHub unavailable"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TF_BUILD", tc.tfBuild)
			warningsFile := filepath.Join(t.TempDir(), "warnings.log")
			t.Setenv(templateCacheWarningsFileEnv, warningsFile)
			t.Setenv(templateCacheDirEnv, t.TempDir())
			emitTemplateCacheWarning("Unable to refresh sample cache")
			require.NoFileExists(t, warningsFile, "only successful fallback warnings should be persisted")
			pointer := "https://github.com/example/samples/blob/main/basic/azure.yaml"
			if tc.cacheHit {
				downloaded := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(downloaded, "azure.yaml"), []byte("name: cached\n"), 0600))
				require.NoError(t, refreshTemplateCache(pointer, downloaded))
				require.NoFileExists(t, warningsFile, "successful downloads should not emit fallback warnings")
			}
			for range 2 {
				err := useCachedTemplateOnDownloadError(pointer, t.TempDir(), tc.downloadErr)
				if tc.cacheHit && !errors.Is(tc.downloadErr, context.Canceled) &&
					!errors.Is(tc.downloadErr, context.DeadlineExceeded) {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, tc.downloadErr)
				}
			}
			if !tc.wantWarning {
				require.NoFileExists(t, warningsFile)
				return
			}
			data, err := os.ReadFile(warningsFile)
			require.NoError(t, err)
			line := "##vso[task.logissue type=warning]GitHub sample download failed; " +
				"using the cached sample. Details: GitHub 503: 50%AZP25%0D%0Aretry%5D\n"
			require.Equal(t, strings.Repeat(line, 2), string(data))
		})
	}
}

func TestTemplateCacheWarningWriteFailureDoesNotFailFallback(t *testing.T) {
	t.Setenv("TF_BUILD", "True")
	t.Setenv(templateCacheWarningsFileEnv, t.TempDir()) // A directory cannot be opened as a warning file.
	t.Setenv(templateCacheDirEnv, t.TempDir())
	pointer := "https://github.com/example/samples/blob/main/basic/azure.yaml"
	downloaded := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(downloaded, "azure.yaml"), []byte("name: cached\n"), 0600))
	require.NoError(t, refreshTemplateCache(pointer, downloaded))
	staging := t.TempDir()
	require.NoError(t, useCachedTemplateOnDownloadError(pointer, staging, errors.New("GitHub unavailable")))
	require.FileExists(t, filepath.Join(staging, "azure.yaml"))
}

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

	t.Run("cancellation is not replaced by cache", func(t *testing.T) {
		staging := t.TempDir()
		err := useCachedTemplateOnDownloadError(pointer, staging, context.Canceled)
		require.ErrorIs(t, err, context.Canceled)
		require.NoFileExists(t, filepath.Join(staging, "azure.yaml"))
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

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
	cacheRoot := t.TempDir()
	t.Setenv(templateCacheDirEnv, cacheRoot)
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

	content, ok := readCachedTemplateManifest(pointer)
	require.True(t, ok)
	require.Contains(t, string(content), "cached-sample")

	updated := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(updated, "azure.yaml"),
		[]byte("name: updated-sample\nservices: {}\n"),
		0600,
	))
	require.NoError(t, refreshTemplateCache(pointer, updated))

	content, ok = readCachedTemplateManifest(pointer)
	require.True(t, ok)
	require.Contains(t, string(content), "updated-sample")

	staging := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(staging, "partial.txt"), []byte("partial"), 0600))
	restored, err := restoreCachedTemplate(pointer, staging)
	require.NoError(t, err)
	require.True(t, restored)
	require.False(t, fileExists(filepath.Join(staging, "partial.txt")))
	require.Contains(t, mustReadFile(t, filepath.Join(staging, "azure.yaml")), "updated-sample")
}

func TestTemplateCacheFallbackMessageRedactsCredentials(t *testing.T) {
	pointer := "https://user:secret@github.com/example/samples/blob/main/azure.yaml?sig=token#fragment"
	message := templateCacheFallbackMessage(pointer, errors.New("download failed: "+pointer))

	require.NotContains(t, message, "user")
	require.NotContains(t, message, "secret")
	require.NotContains(t, message, "sig=token")
	require.NotContains(t, message, "fragment")
	require.Contains(t, message, "https://github.com/example/samples/blob/main/azure.yaml")
}

func TestEscapeAzurePipelinesMessage(t *testing.T) {
	require.Equal(t, "failure%AZP25%0D%0Adetail%5D", escapeAzurePipelinesMessage("failure%\r\ndetail]"))
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(content)
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
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

	content, ok := readCachedTemplateManifest(pointer)
	require.True(t, ok)
	require.Contains(t, string(content), "cached-sample")

	staging := t.TempDir()
	restored, err := restoreCachedTemplate(pointer, staging)
	require.NoError(t, err)
	require.True(t, restored)
	require.True(t, fileExists(filepath.Join(staging, "src", "main.py")))
}

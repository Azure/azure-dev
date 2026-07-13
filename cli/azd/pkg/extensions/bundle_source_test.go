// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeBundleRegistry(t *testing.T, dir string, registry *Registry) string {
	t.Helper()

	data, err := json.Marshal(registry)
	require.NoError(t, err)

	registryPath := filepath.Join(dir, BundleRegistryFileName)
	require.NoError(t, os.WriteFile(registryPath, data, 0600))

	return registryPath
}

func TestNewBundleSource_AnchorsRelativeArtifacts(t *testing.T) {
	t.Parallel()

	bundleDir := t.TempDir()
	remoteURL := "https://example.com/ext.zip"
	registry := &Registry{
		SchemaVersion: CurrentRegistrySchemaVersion,
		Extensions: []*ExtensionMetadata{
			{
				Id:          "test.ext",
				DisplayName: "Test",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Artifacts: map[string]ExtensionArtifact{
							"linux/amd64":  {URL: "artifacts/ext-linux-amd64.tar.gz"},
							"darwin/arm64": {URL: remoteURL},
						},
					},
				},
			},
		},
	}
	writeBundleRegistry(t, bundleDir, registry)

	source, err := newBundleSource("bundle", bundleDir)
	require.NoError(t, err)

	exts, err := source.ListExtensions(t.Context())
	require.NoError(t, err)
	require.Len(t, exts, 1)

	artifacts := exts[0].Versions[0].Artifacts

	// Relative artifact URL is anchored to an absolute path within the bundle dir.
	expected := filepath.Join(bundleDir, "artifacts", "ext-linux-amd64.tar.gz")
	require.Equal(t, expected, artifacts["linux/amd64"].URL)
	require.True(t, filepath.IsAbs(artifacts["linux/amd64"].URL))

	// Remote URLs are left untouched.
	require.Equal(t, remoteURL, artifacts["darwin/arm64"].URL)
}

func TestNewBundleSource_LocationAsRegistryFile(t *testing.T) {
	t.Parallel()

	bundleDir := t.TempDir()
	registry := &Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id:          "test.ext",
				DisplayName: "Test",
				Versions: []ExtensionVersion{
					{
						Version:   "1.0.0",
						Artifacts: map[string]ExtensionArtifact{"linux/amd64": {URL: "bin/ext"}},
					},
				},
			},
		},
	}
	registryPath := writeBundleRegistry(t, bundleDir, registry)

	source, err := newBundleSource("bundle", registryPath)
	require.NoError(t, err)

	exts, err := source.ListExtensions(t.Context())
	require.NoError(t, err)
	require.Equal(t, filepath.Join(bundleDir, "bin", "ext"), exts[0].Versions[0].Artifacts["linux/amd64"].URL)
}

func TestNewBundleSource_RejectsPathTraversal(t *testing.T) {
	t.Parallel()

	bundleDir := t.TempDir()
	registry := &Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id:          "test.ext",
				DisplayName: "Test",
				Versions: []ExtensionVersion{
					{
						Version:   "1.0.0",
						Artifacts: map[string]ExtensionArtifact{"linux/amd64": {URL: "../../etc/passwd"}},
					},
				},
			},
		},
	}
	writeBundleRegistry(t, bundleDir, registry)

	_, err := newBundleSource("bundle", bundleDir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside the bundle directory")
}

func TestNewBundleSource_MissingRegistry(t *testing.T) {
	t.Parallel()

	_, err := newBundleSource("bundle", t.TempDir())
	require.Error(t, err)
}

func TestAnchorArtifactURL_AbsolutePathUnchanged(t *testing.T) {
	t.Parallel()

	bundleDir := t.TempDir()
	abs := filepath.Join(bundleDir, "artifacts", "ext")
	if runtime.GOOS == "windows" {
		abs = `C:\some\path\ext`
	}

	result, err := anchorArtifactURL(abs, bundleDir)
	require.NoError(t, err)
	require.Equal(t, abs, result)
}

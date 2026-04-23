// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package containerregistry

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// readArchiveEntries reads all entries from a tar.gz archive and returns a map of entry name to content.
func readArchiveEntries(t *testing.T, archivePath string) map[string]string {
	t.Helper()

	f, err := os.Open(archivePath)
	require.NoError(t, err)
	defer f.Close()

	gr, err := gzip.NewReader(f)
	require.NoError(t, err)
	defer gr.Close()

	tr := tar.NewReader(gr)
	entries := make(map[string]string)

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)

		content, err := io.ReadAll(tr)
		require.NoError(t, err)

		entries[hdr.Name] = string(content)
	}

	return entries
}

func TestPackRemoteBuildSource_BasicContext(t *testing.T) {
	root := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM alpine"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package main"), 0600))

	archivePath, dockerfilePath, err := PackRemoteBuildSource(
		context.Background(), root, filepath.Join(root, "Dockerfile"),
	)
	if archivePath != "" {
		defer os.Remove(archivePath)
	}

	require.NoError(t, err)
	require.NotEmpty(t, archivePath)
	require.Equal(t, "Dockerfile", dockerfilePath)

	entries := readArchiveEntries(t, archivePath)
	require.Contains(t, entries, "Dockerfile")
	require.Contains(t, entries, "main.go")
	require.Equal(t, "FROM alpine", entries["Dockerfile"])
	require.Equal(t, "package main", entries["main.go"])
}

func TestPackRemoteBuildSource_ExcludesGitDirectory(t *testing.T) {
	root := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM alpine"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git", "objects"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/main"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "app.go"), []byte("package app"), 0600))

	archivePath, _, err := PackRemoteBuildSource(
		context.Background(), root, filepath.Join(root, "Dockerfile"),
	)
	if archivePath != "" {
		defer os.Remove(archivePath)
	}

	require.NoError(t, err)

	entries := readArchiveEntries(t, archivePath)
	require.Contains(t, entries, "Dockerfile")
	require.Contains(t, entries, "app.go")

	for name := range entries {
		require.False(t, strings.HasPrefix(name, ".git/"),
			"expected .git directory to be excluded, but found entry: %s", name)
	}
}

func TestPackRemoteBuildSource_DockerignoreExcludesFiles(t *testing.T) {
	root := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM alpine"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package main"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "secret.env"), []byte("SECRET=value"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".dockerignore"), []byte("*.env\n"), 0600))

	archivePath, _, err := PackRemoteBuildSource(
		context.Background(), root, filepath.Join(root, "Dockerfile"),
	)
	if archivePath != "" {
		defer os.Remove(archivePath)
	}

	require.NoError(t, err)

	entries := readArchiveEntries(t, archivePath)
	require.Contains(t, entries, "Dockerfile")
	require.Contains(t, entries, "main.go")
	require.NotContains(t, entries, "secret.env")
}

func TestPackRemoteBuildSource_DockerfileSpecificDockerignore(t *testing.T) {
	root := t.TempDir()

	dockerfilePath := filepath.Join(root, "Dockerfile.prod")
	require.NoError(t, os.WriteFile(dockerfilePath, []byte("FROM alpine"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package main"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "test_data.txt"), []byte("test"), 0600))

	// Dockerfile-specific .dockerignore takes precedence over root .dockerignore
	require.NoError(t, os.WriteFile(dockerfilePath+".dockerignore", []byte("test_data.txt\n"), 0600))
	// Root .dockerignore would exclude main.go — but should NOT be used when dockerfile-specific exists
	require.NoError(t, os.WriteFile(filepath.Join(root, ".dockerignore"), []byte("main.go\n"), 0600))

	archivePath, _, err := PackRemoteBuildSource(context.Background(), root, dockerfilePath)
	if archivePath != "" {
		defer os.Remove(archivePath)
	}

	require.NoError(t, err)

	entries := readArchiveEntries(t, archivePath)
	require.Contains(t, entries, "main.go", "dockerfile-specific ignore should not exclude main.go")
	require.NotContains(t, entries, "test_data.txt", "dockerfile-specific ignore should exclude test_data.txt")
}

func TestPackRemoteBuildSource_DockerfileOutsideContext(t *testing.T) {
	root := t.TempDir()
	externalDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package main"), 0600))

	externalDockerfile := filepath.Join(externalDir, "Dockerfile")
	require.NoError(t, os.WriteFile(externalDockerfile, []byte("FROM ubuntu"), 0600))

	archivePath, dockerfileArchivePath, err := PackRemoteBuildSource(
		context.Background(), root, externalDockerfile,
	)
	if archivePath != "" {
		defer os.Remove(archivePath)
	}

	require.NoError(t, err)
	require.NotEmpty(t, dockerfileArchivePath)
	require.True(t, strings.HasSuffix(dockerfileArchivePath, "_Dockerfile"),
		"expected dockerfile archive path to end with _Dockerfile, got: %s", dockerfileArchivePath)

	entries := readArchiveEntries(t, archivePath)
	require.Contains(t, entries, "main.go")
	require.Contains(t, entries, dockerfileArchivePath)
	require.Equal(t, "FROM ubuntu", entries[dockerfileArchivePath])
}

func TestPackRemoteBuildSource_SubdirectoriesIncluded(t *testing.T) {
	root := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(root, "src", "pkg"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM alpine"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "pkg", "util.go"), []byte("package pkg"), 0600))

	archivePath, _, err := PackRemoteBuildSource(
		context.Background(), root, filepath.Join(root, "Dockerfile"),
	)
	if archivePath != "" {
		defer os.Remove(archivePath)
	}

	require.NoError(t, err)

	entries := readArchiveEntries(t, archivePath)
	require.Contains(t, entries, "Dockerfile")
	require.Contains(t, entries, "src/main.go")
	require.Contains(t, entries, "src/pkg/util.go")
}

func TestPackRemoteBuildSource_EmptyContextWithDockerfileOnly(t *testing.T) {
	root := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch"), 0600))

	archivePath, dockerfilePath, err := PackRemoteBuildSource(
		context.Background(), root, filepath.Join(root, "Dockerfile"),
	)
	if archivePath != "" {
		defer os.Remove(archivePath)
	}

	require.NoError(t, err)
	require.Equal(t, "Dockerfile", dockerfilePath)

	entries := readArchiveEntries(t, archivePath)
	require.Len(t, entries, 1)
	require.Equal(t, "FROM scratch", entries["Dockerfile"])
}

func TestNewRemoteBuildManager(t *testing.T) {
	mgr := NewRemoteBuildManager(nil, nil)
	require.NotNil(t, mgr)
}

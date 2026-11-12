// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeContainedDocumentUsesWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "services/search.yaml", "category: CognitiveSearch\n")
	require.NoError(t, os.Mkdir(filepath.Join(root, "services", "nested"), 0o700))
	t.Chdir(filepath.Join(root, "services", "nested"))

	got, display, err := DecodeContainedDocument(root, filepath.Join("..", "search.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "services/search.yaml", display)
	assert.Equal(t, map[string]any{"category": "CognitiveSearch"}, got)
}

func TestReadContainedFileRejectsOutsideProject(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeProjectFile(t, outside, "private.yaml", "marker: secret\n")

	_, _, err := ReadContainedFile(root, filepath.Join(outside, "private.yaml"))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), outside)
	assert.NotContains(t, err.Error(), "secret")
}

func TestResolveContainedFileRefs(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "connections/base.yaml", `
name: search
credentials:
  key: value
`)
	writeProjectFile(t, root, "connections/outer.yaml", `
$ref: ./base.yaml
target: https://search.example
`)
	writeProjectFile(t, root, "services/entry.yaml", `
connection:
  $ref: ../connections/outer.yaml
`)

	got, err := ResolveContainedFileRefs(map[string]any{
		"$ref": "./services/entry.yaml",
	}, root)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"connection": map[string]any{
			"name":   "search",
			"target": "https://search.example",
			"credentials": map[string]any{
				"key": "value",
			},
		},
	}, got)
}

func TestResolveContainedFileRefsFromFileUsesSourceDirectory(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "connections/search.yaml", `
category: CognitiveSearch
target: https://search.example
credentials:
  $ref: ./credentials/base.yaml
`)
	writeProjectFile(t, root, "connections/credentials/base.yaml", `
$ref: ./keys.yaml
`)
	writeProjectFile(t, root, "connections/credentials/keys.yaml", `
key: ${SEARCH_KEY}
`)

	sourcePath := filepath.Join(root, "connections", "search.yaml")
	document, display, err := DecodeContainedDocument(root, sourcePath)
	require.NoError(t, err)

	got, err := ResolveContainedFileRefsFromFile(
		document,
		root,
		filepath.Join(root, filepath.FromSlash(display)),
	)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"category": "CognitiveSearch",
		"target":   "https://search.example",
		"credentials": map[string]any{
			"key": "${SEARCH_KEY}",
		},
	}, got)
}

func TestResolveContainedFileRefsRejectsRemoteAndOutsideTargets(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeProjectFile(t, outside, "secret.yaml", "token: marker\n")

	refs := []string{
		"https://user:password@example.test/ref.yaml?sig=secret#private",
		filepath.Join(outside, "secret.yaml"),
		filepath.Join("..", filepath.Base(outside), "secret.yaml"),
	}
	for _, ref := range refs {
		t.Run(filepath.Base(ref), func(t *testing.T) {
			_, err := ResolveContainedFileRefs(map[string]any{"$ref": ref}, root)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "user")
			assert.NotContains(t, err.Error(), "password")
			assert.NotContains(t, err.Error(), "sig=secret")
			assert.NotContains(t, err.Error(), "private")
			assert.NotContains(t, err.Error(), outside)
			assert.NotContains(t, err.Error(), "marker")
		})
	}
}

func TestResolveContainedFileRefsRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeProjectFile(t, outside, "secret.yaml", "token: marker\n")
	link := filepath.Join(root, "links")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	_, err := ResolveContainedFileRefs(map[string]any{
		"$ref": "./links/secret.yaml",
	}, root)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), outside)
	assert.NotContains(t, err.Error(), "marker")
}

func TestResolveContainedFileRefsAllowsInternalSymlink(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "targets/connection.yaml", `
category: CognitiveSearch
target: https://search.example
`)
	require.NoError(t, os.Mkdir(filepath.Join(root, "aliases"), 0o700))
	link := filepath.Join(root, "aliases", "connection.yaml")
	if err := os.Symlink(filepath.Join(root, "targets", "connection.yaml"), link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	got, err := ResolveContainedFileRefs(map[string]any{
		"$ref": "./aliases/connection.yaml",
	}, root)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"category": "CognitiveSearch",
		"target":   "https://search.example",
	}, got)
}

func TestResolveContainedFileRefsRejectsCyclesAndMultipleDocuments(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "cycle-a.yaml", "next:\n  $ref: ./cycle-b.yaml\n")
	writeProjectFile(t, root, "cycle-b.yaml", "next:\n  $ref: ./cycle-a.yaml\n")
	writeProjectFile(t, root, "multiple.yaml", "name: first\n---\nname: second\n")

	_, err := ResolveContainedFileRefs(map[string]any{"$ref": "./cycle-a.yaml"}, root)
	require.ErrorContains(t, err, "cyclic")
	assert.NotContains(t, err.Error(), root)

	_, err = ResolveContainedFileRefs(map[string]any{"$ref": "./multiple.yaml"}, root)
	require.ErrorContains(t, err, "one YAML or JSON object")
	assert.NotContains(t, err.Error(), root)
}

func TestResolveContainedFileRefsErrorDoesNotExposeMalformedContent(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "bad.yaml", "token: private-marker\n: malformed\n")

	_, err := ResolveContainedFileRefs(map[string]any{"$ref": "./bad.yaml"}, root)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "private-marker")
	assert.NotContains(t, err.Error(), "malformed")
}

func TestResolveContainedFileRefsLimitsDepth(t *testing.T) {
	root := t.TempDir()
	for index := range maxRefDepth + 1 {
		next := fmt.Sprintf("file-%d.yaml", index+1)
		if index == maxRefDepth {
			writeProjectFile(t, root, fmt.Sprintf("file-%d.yaml", index), "name: last\n")
			continue
		}
		writeProjectFile(t, root, fmt.Sprintf("file-%d.yaml", index), "$ref: "+next+"\n")
	}

	_, err := ResolveContainedFileRefs(map[string]any{"$ref": "file-0.yaml"}, root)
	require.ErrorContains(t, err, "nesting exceeds")
}

func writeProjectFile(t *testing.T, root, relativePath, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(strings.TrimLeft(content, "\n")), 0o600))
}

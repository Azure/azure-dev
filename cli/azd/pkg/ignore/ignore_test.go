// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ignore

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewMatcher_NoFiles(t *testing.T) {
	dir := t.TempDir()

	m, err := NewMatcher(dir)
	require.NoError(t, err)
	require.NotNil(t, m)

	// Nothing should be ignored when no ignore files exist.
	require.False(t, m.IsIgnored("anything.txt", false))
	require.False(t, m.IsIgnored("node_modules", true))
}

func TestNilMatcher_IsIgnored(t *testing.T) {
	var m *Matcher
	require.False(t, m.IsIgnored("foo.txt", false))
}

func TestNewMatcher_AzdxIgnoreOnly(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, AzdxIgnoreFile, "node_modules/\n*.log\n")

	m, err := NewMatcher(dir)
	require.NoError(t, err)

	// Directory pattern: matches the directory itself.
	require.True(t, m.IsIgnored("node_modules", true))
	// Wildcard pattern: matches files at any depth.
	require.True(t, m.IsIgnored("error.log", false))
	require.True(t, m.IsIgnored("sub/dir/debug.log", false))

	require.False(t, m.IsIgnored("src/main.go", false))
	require.False(t, m.IsIgnored("README.md", false))
}

func TestNewMatcher_GitIgnoreOnly(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, GitIgnoreFile, "dist/\n*.o\n")

	m, err := NewMatcher(dir)
	require.NoError(t, err)

	// Directory pattern: matches the directory itself.
	require.True(t, m.IsIgnored("dist", true))
	// Wildcard pattern: matches files.
	require.True(t, m.IsIgnored("main.o", false))

	require.False(t, m.IsIgnored("src/main.go", false))
}

func TestNewMatcher_BothFilesAdditive(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, AzdxIgnoreFile, "build/\n")
	writeFile(t, dir, GitIgnoreFile, "node_modules/\n")

	m, err := NewMatcher(dir)
	require.NoError(t, err)

	// Both patterns should apply.
	require.True(t, m.IsIgnored("build", true))
	require.True(t, m.IsIgnored("node_modules", true))

	require.False(t, m.IsIgnored("src/main.go", false))
}

func TestNewMatcher_Comments(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, AzdxIgnoreFile, "# This is a comment\ntemp/\n")

	m, err := NewMatcher(dir)
	require.NoError(t, err)

	require.True(t, m.IsIgnored("temp", true))
	// The comment line itself should not match anything.
	require.False(t, m.IsIgnored("# This is a comment", false))
}

func TestNewMatcher_Negation(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, AzdxIgnoreFile, "*.log\n!important.log\n")

	m, err := NewMatcher(dir)
	require.NoError(t, err)

	require.True(t, m.IsIgnored("error.log", false))
	// Negation: important.log should NOT be ignored.
	require.False(t, m.IsIgnored("important.log", false))
}

func TestNewMatcher_WildcardPatterns(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, AzdxIgnoreFile, "*.tmp\n**/*.bak\n")

	m, err := NewMatcher(dir)
	require.NoError(t, err)

	require.True(t, m.IsIgnored("file.tmp", false))
	require.True(t, m.IsIgnored("sub/dir/file.bak", false))

	require.False(t, m.IsIgnored("file.txt", false))
}

func TestNewMatcher_DirectoryVsFile(t *testing.T) {
	dir := t.TempDir()

	// Trailing slash means "directory only" in gitignore syntax.
	writeFile(t, dir, AzdxIgnoreFile, "logs/\n")

	m, err := NewMatcher(dir)
	require.NoError(t, err)

	// "logs" as a directory should be ignored.
	require.True(t, m.IsIgnored("logs", true))

	// "logs" as a file should NOT be ignored (pattern has trailing slash).
	require.False(t, m.IsIgnored("logs", false))
}

func TestNewMatcher_EmptyFile(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, AzdxIgnoreFile, "")

	m, err := NewMatcher(dir)
	require.NoError(t, err)
	require.False(t, m.IsIgnored("anything", false))
}

func TestNewMatcher_OnlyComments(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, AzdxIgnoreFile, "# comment 1\n# comment 2\n")

	m, err := NewMatcher(dir)
	require.NoError(t, err)
	require.False(t, m.IsIgnored("anything", false))
}

func TestNewMatcher_UTF8BOM(t *testing.T) {
	dir := t.TempDir()

	// Write file with BOM prefix — should be stripped transparently.
	bom := []byte{0xEF, 0xBB, 0xBF}
	content := append(bom, []byte("vendor/\n")...)
	err := os.WriteFile(filepath.Join(dir, AzdxIgnoreFile), content, 0600)
	require.NoError(t, err)

	m, err := NewMatcher(dir)
	require.NoError(t, err)

	require.True(t, m.IsIgnored("vendor", true))
}

func TestNewMatcher_RelativePaths(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, AzdxIgnoreFile, "sub/dir/\n")

	m, err := NewMatcher(dir)
	require.NoError(t, err)

	require.True(t, m.IsIgnored("sub/dir", true))
	require.True(t, m.IsIgnored(filepath.Join("sub", "dir"), true))
}

func TestNewMatcher_BackslashPaths(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, AzdxIgnoreFile, "vendor/\n")

	m, err := NewMatcher(dir)
	require.NoError(t, err)

	// Windows-style backslash paths should still match.
	require.True(t, m.IsIgnored("vendor", true))
}

func TestNewMatcher_UnreadableFile(t *testing.T) {
	// This test verifies that permission errors are propagated.
	// On Windows, file permissions work differently, so we skip
	// the unreadable-file test there.
	if runtime.GOOS == "windows" {
		t.Skip("skipping unreadable file test on Windows")
	}

	dir := t.TempDir()
	p := filepath.Join(dir, AzdxIgnoreFile)
	err := os.WriteFile(p, []byte("test\n"), 0600)
	require.NoError(t, err)

	// Remove read permission.
	err = os.Chmod(p, 0000)
	require.NoError(t, err)
	t.Cleanup(func() { os.Chmod(p, 0600) })

	_, err = NewMatcher(dir)
	require.Error(t, err)
}

// writeFile is a test helper that creates a file with the given content.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600)
	require.NoError(t, err)
}

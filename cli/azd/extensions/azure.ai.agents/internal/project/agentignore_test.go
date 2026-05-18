// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentIgnore_NoFile_UsesDefaults(t *testing.T) {
	dir := t.TempDir()

	m, err := newAgentIgnoreMatcher(dir)
	require.NoError(t, err)
	require.False(t, m.hasUserIgnore)

	// Default exclusions
	require.True(t, m.ShouldExclude("__pycache__", true))
	require.True(t, m.ShouldExclude(".venv", true))
	require.True(t, m.ShouldExclude("venv", true))
	require.True(t, m.ShouldExclude("node_modules", true))
	require.True(t, m.ShouldExclude("bin", true))
	require.True(t, m.ShouldExclude("obj", true))
	require.True(t, m.ShouldExclude(".vs", true))
	require.True(t, m.ShouldExclude(".git", true))
	require.True(t, m.ShouldExclude(".mypy_cache", true))
	require.True(t, m.ShouldExclude(".pytest_cache", true))
	require.True(t, m.ShouldExclude("foo.pyc", false))
	require.True(t, m.ShouldExclude("bar.pyo", false))
	require.True(t, m.ShouldExclude("x.user", false))
	require.True(t, m.ShouldExclude("y.suo", false))
	require.True(t, m.ShouldExclude("agent.yaml", false))
	require.True(t, m.ShouldExclude("agent.manifest.yaml", false))
	require.True(t, m.ShouldExclude("azure.yaml", false))
	require.True(t, m.ShouldExclude(".agentignore", false))

	// Should NOT exclude normal files
	require.False(t, m.ShouldExclude("main.py", false))
	require.False(t, m.ShouldExclude("requirements.txt", false))
	require.False(t, m.ShouldExclude("src", true))
}

func TestAgentIgnore_SecurityAlwaysExcluded(t *testing.T) {
	dir := t.TempDir()
	// Create .agentignore that tries to negate security files
	err := os.WriteFile(filepath.Join(dir, ".agentignore"), []byte("!.env\n!.azure/\n!.git/\n"), 0600)
	require.NoError(t, err)

	m, err := newAgentIgnoreMatcher(dir)
	require.NoError(t, err)
	require.True(t, m.hasUserIgnore)

	// Security exclusions cannot be overridden
	require.True(t, m.ShouldExclude(".env", false))
	require.True(t, m.ShouldExclude(".env.local", false))
	require.True(t, m.ShouldExclude(".env.production", false))
	require.True(t, m.ShouldExclude(".azure", true))
	require.True(t, m.ShouldExclude(".git", true))

	// Nested .env files are also excluded
	require.True(t, m.ShouldExclude("config/.env", false))
	require.True(t, m.ShouldExclude("config/.env.local", false))

	// Files inside .azure/ and .git/ are excluded
	require.True(t, m.ShouldExclude(".azure/config", false))
	require.True(t, m.ShouldExclude(".git/HEAD", false))
}

func TestAgentIgnore_UserFileOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	// User only excludes *.log — defaults like __pycache__ should NOT apply
	err := os.WriteFile(filepath.Join(dir, ".agentignore"), []byte("*.log\n"), 0600)
	require.NoError(t, err)

	m, err := newAgentIgnoreMatcher(dir)
	require.NoError(t, err)
	require.True(t, m.hasUserIgnore)

	// User-specified exclusion works
	require.True(t, m.ShouldExclude("debug.log", false))

	// Default exclusions no longer apply (user took control)
	require.False(t, m.ShouldExclude("__pycache__", true))
	require.False(t, m.ShouldExclude("node_modules", true))
	require.False(t, m.ShouldExclude("foo.pyc", false))

	// Security still applies
	require.True(t, m.ShouldExclude(".env", false))
	require.True(t, m.ShouldExclude(".git", true))
}

func TestAgentIgnore_NegationWorks(t *testing.T) {
	dir := t.TempDir()
	// Exclude all .txt but keep important.txt
	content := "*.txt\n!important.txt\n"
	err := os.WriteFile(filepath.Join(dir, ".agentignore"), []byte(content), 0600)
	require.NoError(t, err)

	m, err := newAgentIgnoreMatcher(dir)
	require.NoError(t, err)

	require.True(t, m.ShouldExclude("notes.txt", false))
	require.False(t, m.ShouldExclude("important.txt", false))
}

func TestAgentIgnore_SymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real_ignore")
	err := os.WriteFile(target, []byte("*.log\n"), 0600)
	require.NoError(t, err)

	link := filepath.Join(dir, ".agentignore")
	err = os.Symlink(target, link)
	if err != nil {
		t.Skip("symlinks not supported on this platform")
	}

	_, err = newAgentIgnoreMatcher(dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be a regular file")
}

func TestAgentIgnore_UTF8BOM(t *testing.T) {
	dir := t.TempDir()
	// Write file with UTF-8 BOM
	content := append([]byte{0xEF, 0xBB, 0xBF}, []byte("*.log\n")...)
	err := os.WriteFile(filepath.Join(dir, ".agentignore"), content, 0600)
	require.NoError(t, err)

	m, err := newAgentIgnoreMatcher(dir)
	require.NoError(t, err)

	require.True(t, m.ShouldExclude("app.log", false))
}

func TestAgentIgnore_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, ".agentignore"), []byte(""), 0600)
	require.NoError(t, err)

	m, err := newAgentIgnoreMatcher(dir)
	require.NoError(t, err)
	require.True(t, m.hasUserIgnore)

	// Nothing excluded except security
	require.False(t, m.ShouldExclude("main.py", false))
	require.False(t, m.ShouldExclude("__pycache__", true))
	require.True(t, m.ShouldExclude(".env", false))
}

func TestDefaultAgentIgnoreContent(t *testing.T) {
	content := DefaultAgentIgnoreContent()
	require.Contains(t, content, "__pycache__/")
	require.Contains(t, content, "node_modules/")
	require.Contains(t, content, "agent.yaml")
	require.Contains(t, content, ".agentignore")
	require.Contains(t, content, "bin/")
}

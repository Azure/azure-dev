// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentIgnore_NoFile_UsesDefaults(t *testing.T) {
	dir := t.TempDir()

	m, err := newAgentIgnoreMatcher(t.Context(), dir)
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
	require.True(t, m.ShouldExclude("connection.yaml", false))
	require.True(t, m.ShouldExclude("connection.yml", false))
	require.True(t, m.ShouldExclude("toolbox.yaml", false))
	require.True(t, m.ShouldExclude("toolbox.yml", false))
	require.True(t, m.ShouldExclude(".agentignore", false))
	require.True(t, m.ShouldExclude(".env", false))
	require.True(t, m.ShouldExclude(".env.local", false))
	require.True(t, m.ShouldExclude(".azure", true))
	require.True(t, m.ShouldExclude("Dockerfile", false))
	require.True(t, m.ShouldExclude(".dockerignore", false))

	// Should NOT exclude normal files
	require.False(t, m.ShouldExclude("main.py", false))
	require.False(t, m.ShouldExclude("requirements.txt", false))
	require.False(t, m.ShouldExclude("src", true))
}

func TestAgentIgnore_UserFileOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	// User only excludes *.log — defaults like __pycache__ should NOT apply
	err := os.WriteFile(filepath.Join(dir, ".agentignore"), []byte("*.log\n"), 0600)
	require.NoError(t, err)

	m, err := newAgentIgnoreMatcher(t.Context(), dir)
	require.NoError(t, err)
	require.True(t, m.hasUserIgnore)

	// User-specified exclusion works
	require.True(t, m.ShouldExclude("debug.log", false))

	// Default exclusions no longer apply (user took control)
	require.False(t, m.ShouldExclude("__pycache__", true))
	require.False(t, m.ShouldExclude("node_modules", true))
	require.False(t, m.ShouldExclude("foo.pyc", false))

	// Security files are also user-configurable now — not excluded unless user lists them
	require.False(t, m.ShouldExclude(".env", false))
	require.False(t, m.ShouldExclude(".git", true))

	// Canonical resource definitions are azd-managed defaults so existing
	// .agentignore files do not accidentally package Connection credentials.
	require.True(t, m.ShouldExclude("connection.yaml", false))
	require.True(t, m.ShouldExclude("toolbox.yaml", false))
}

func TestAgentIgnore_AlwaysExcludesGeneratedTeamsArtifacts(t *testing.T) {
	for _, name := range []string{
		"appPackage.zip",
		"build/appPackage.zip",
		".appPackage.zip.azd-generated",
		"build/.appPackage.zip.azd-generated",
		"TEAMS_APP_SETUP.md",
		"build/TEAMS_APP_SETUP.md",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			err := os.WriteFile(filepath.Join(dir, ".agentignore"), []byte(""), 0600)
			require.NoError(t, err)

			m, err := newAgentIgnoreMatcher(t.Context(), dir)
			require.NoError(t, err)
			require.True(t, m.hasUserIgnore)
			require.True(t, m.ShouldExclude(name, false))
		})
	}
}

func TestAgentIgnore_GeneratedTeamsArtifactsCanBeNegated(t *testing.T) {
	dir := t.TempDir()
	content := "!appPackage.zip\n!TEAMS_APP_SETUP.md\n"
	err := os.WriteFile(filepath.Join(dir, ".agentignore"), []byte(content), 0600)
	require.NoError(t, err)

	m, err := newAgentIgnoreMatcher(t.Context(), dir)
	require.NoError(t, err)
	require.True(t, m.hasUserIgnore)
	require.False(t, m.ShouldExclude("appPackage.zip", false))
	require.False(t, m.ShouldExclude("build/appPackage.zip", false))
	require.False(t, m.ShouldExclude("TEAMS_APP_SETUP.md", false))
	require.False(t, m.ShouldExclude("build/TEAMS_APP_SETUP.md", false))
	require.True(t, m.ShouldExclude(".appPackage.zip.azd-generated", false))
}

func TestAgentIgnore_ResourceDefinitionsCanBeNegated(t *testing.T) {
	dir := t.TempDir()
	content := "!connection.yaml\n!toolbox.yaml\n"
	err := os.WriteFile(filepath.Join(dir, ".agentignore"), []byte(content), 0600)
	require.NoError(t, err)

	m, err := newAgentIgnoreMatcher(t.Context(), dir)
	require.NoError(t, err)
	require.False(t, m.ShouldExclude("connection.yaml", false))
	require.False(t, m.ShouldExclude("toolbox.yaml", false))
}

func TestZipSourceDirExcludesResourceDefinitionsWithExistingIgnore(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"main.py":         "print('ready')\n",
		"agent.yaml":      "name: test\n",
		"connection.yaml": "credentials:\n  key: secret\n",
		"toolbox.yaml":    "name: tools\n",
		// Simulates an .agentignore generated before the resource definitions
		// became separate canonical files.
		".agentignore": "agent.yaml\n.agentignore\n",
	}
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0600))
	}

	zipPath, _, err := zipSourceDir(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(zipPath) })

	archive, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = archive.Close() })
	names := make([]string, len(archive.File))
	for i, file := range archive.File {
		names[i] = file.Name
	}
	require.Equal(t, []string{"main.py"}, names)
}

func TestAgentIgnore_NegationWorks(t *testing.T) {
	dir := t.TempDir()
	// Exclude all .txt but keep important.txt
	content := "*.txt\n!important.txt\n"
	err := os.WriteFile(filepath.Join(dir, ".agentignore"), []byte(content), 0600)
	require.NoError(t, err)

	m, err := newAgentIgnoreMatcher(t.Context(), dir)
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

	_, err = newAgentIgnoreMatcher(t.Context(), dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be a regular file")
}

func TestAgentIgnore_UTF8BOM(t *testing.T) {
	dir := t.TempDir()
	// Write file with UTF-8 BOM
	content := append([]byte{0xEF, 0xBB, 0xBF}, []byte("*.log\n")...)
	err := os.WriteFile(filepath.Join(dir, ".agentignore"), content, 0600)
	require.NoError(t, err)

	m, err := newAgentIgnoreMatcher(t.Context(), dir)
	require.NoError(t, err)

	require.True(t, m.ShouldExclude("app.log", false))
}

func TestAgentIgnore_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, ".agentignore"), []byte(""), 0600)
	require.NoError(t, err)

	m, err := newAgentIgnoreMatcher(t.Context(), dir)
	require.NoError(t, err)
	require.True(t, m.hasUserIgnore)

	// Defaults no longer apply, but azd-generated Teams artifacts are prepended
	// to avoid bundling them into code deploy packages.
	require.False(t, m.ShouldExclude("main.py", false))
	require.False(t, m.ShouldExclude("__pycache__", true))
	require.False(t, m.ShouldExclude(".env", false))
	require.True(t, m.ShouldExclude("appPackage.zip", false))
}

func TestDefaultAgentIgnoreContent(t *testing.T) {
	content := DefaultAgentIgnoreContent()
	require.Contains(t, content, "__pycache__/")
	require.Contains(t, content, "node_modules/")
	require.Contains(t, content, "agent.yaml")
	require.Contains(t, content, "connection.yaml")
	require.Contains(t, content, "connection.yml")
	require.Contains(t, content, "toolbox.yaml")
	require.Contains(t, content, "toolbox.yml")
	require.Contains(t, content, ".agentignore")
	require.Contains(t, content, "bin/")
	require.Contains(t, content, ".env")
	require.Contains(t, content, ".azure/")
	require.Contains(t, content, ".git/")
	require.Contains(t, content, "Dockerfile")
	require.Contains(t, content, ".dockerignore")
	require.Contains(t, content, "appPackage.zip")
	require.Contains(t, content, ".appPackage.zip.azd-generated")
	require.Contains(t, content, "TEAMS_APP_SETUP.md")
}

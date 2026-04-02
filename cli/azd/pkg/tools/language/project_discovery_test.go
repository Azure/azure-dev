// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package language

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverProjectFile_Python(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "hook.py")
	projectFile := filepath.Join(dir, "requirements.txt")

	writeFile(t, projectFile, "flask\n")

	result, err := DiscoverProjectFile(scriptPath, dir)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, dir, result.ProjectDir)
	assert.Equal(t, projectFile, result.DependencyFile)
	assert.Equal(t, ScriptLanguagePython, result.Language)
}

func TestDiscoverProjectFile_PythonPyproject(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "hook.py")
	projectFile := filepath.Join(dir, "pyproject.toml")

	writeFile(t, projectFile, "[project]\n")

	result, err := DiscoverProjectFile(scriptPath, dir)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, dir, result.ProjectDir)
	assert.Equal(t, projectFile, result.DependencyFile)
	assert.Equal(t, ScriptLanguagePython, result.Language)
}

func TestDiscoverProjectFile_JavaScript(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "hook.js")
	projectFile := filepath.Join(dir, "package.json")

	writeFile(t, projectFile, "{}\n")

	result, err := DiscoverProjectFile(scriptPath, dir)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, dir, result.ProjectDir)
	assert.Equal(t, projectFile, result.DependencyFile)
	assert.Equal(t, ScriptLanguageJavaScript, result.Language)
}

func TestDiscoverProjectFile_DotNet(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "hook.cs")
	projectFile := filepath.Join(dir, "test.csproj")

	writeFile(t, projectFile, "<Project />\n")

	result, err := DiscoverProjectFile(scriptPath, dir)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, dir, result.ProjectDir)
	assert.Equal(t, projectFile, result.DependencyFile)
	assert.Equal(t, ScriptLanguageDotNet, result.Language)
}

func TestDiscoverProjectFile_DotNetFsProj(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "hook.fsx")
	projectFile := filepath.Join(dir, "test.fsproj")

	writeFile(t, projectFile, "<Project />\n")

	result, err := DiscoverProjectFile(scriptPath, dir)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, dir, result.ProjectDir)
	assert.Equal(t, projectFile, result.DependencyFile)
	assert.Equal(t, ScriptLanguageDotNet, result.Language)
}

func TestDiscoverProjectFile_WalkUp(t *testing.T) {
	// Project file in parent, script in child subdirectory.
	//
	// dir/
	//   requirements.txt
	//   hooks/
	//     hook.py          <- script starts here
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, "hooks")
	require.NoError(t, os.MkdirAll(hooksDir, 0o700))

	projectFile := filepath.Join(dir, "requirements.txt")
	writeFile(t, projectFile, "flask\n")

	scriptPath := filepath.Join(hooksDir, "hook.py")

	result, err := DiscoverProjectFile(scriptPath, dir)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, dir, result.ProjectDir)
	assert.Equal(t, projectFile, result.DependencyFile)
	assert.Equal(t, ScriptLanguagePython, result.Language)
}

func TestDiscoverProjectFile_BoundaryRespected(t *testing.T) {
	// Project file exists above the boundary, so it must NOT be found.
	//
	// root/
	//   requirements.txt   <- above boundary
	//   child/             <- boundary
	//     hook.py          <- script starts here
	root := t.TempDir()
	child := filepath.Join(root, "child")
	require.NoError(t, os.MkdirAll(child, 0o700))

	// Place project file above the boundary.
	writeFile(t, filepath.Join(root, "requirements.txt"), "flask\n")

	scriptPath := filepath.Join(child, "hook.py")

	result, err := DiscoverProjectFile(scriptPath, child)

	require.NoError(t, err)
	assert.Nil(t, result, "project file above boundary must not be found")
}

func TestDiscoverProjectFile_NoProjectFile(t *testing.T) {
	// Empty directory — no project files anywhere.
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "hook.py")

	result, err := DiscoverProjectFile(scriptPath, dir)

	require.NoError(t, err)
	assert.Nil(t, result, "expected nil when no project file exists")
}

func TestDiscoverProjectFile_Priority(t *testing.T) {
	// Multiple project files in the same directory — the one with the
	// highest priority (earliest in knownProjectFiles) should win.
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "hook.py")

	// Create both Python and JavaScript project files.
	reqFile := filepath.Join(dir, "requirements.txt")
	writeFile(t, reqFile, "flask\n")
	writeFile(t, filepath.Join(dir, "package.json"), "{}\n")

	result, err := DiscoverProjectFile(scriptPath, dir)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, ScriptLanguagePython, result.Language,
		"requirements.txt has higher priority than package.json")
	assert.Equal(t, reqFile, result.DependencyFile)
}

func TestDiscoverProjectFile_WalkUpMultipleLevels(t *testing.T) {
	// Script is nested several levels deep; project file is at root.
	//
	// root/
	//   package.json
	//   a/
	//     b/
	//       hook.js        <- script starts here
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	require.NoError(t, os.MkdirAll(deep, 0o700))

	projectFile := filepath.Join(root, "package.json")
	writeFile(t, projectFile, "{}\n")

	scriptPath := filepath.Join(deep, "hook.js")

	result, err := DiscoverProjectFile(scriptPath, root)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, root, result.ProjectDir)
	assert.Equal(t, projectFile, result.DependencyFile)
	assert.Equal(t, ScriptLanguageJavaScript, result.Language)
}

func TestDiscoverProjectFile_ClosestWins(t *testing.T) {
	// Project files at multiple levels — the closest to the script wins.
	//
	// root/
	//   requirements.txt   <- farther
	//   child/
	//     package.json     <- closer to script
	//     hook.js
	root := t.TempDir()
	child := filepath.Join(root, "child")
	require.NoError(t, os.MkdirAll(child, 0o700))

	writeFile(t, filepath.Join(root, "requirements.txt"), "flask\n")
	closerFile := filepath.Join(child, "package.json")
	writeFile(t, closerFile, "{}\n")

	scriptPath := filepath.Join(child, "hook.js")

	result, err := DiscoverProjectFile(scriptPath, root)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, child, result.ProjectDir)
	assert.Equal(t, closerFile, result.DependencyFile)
	assert.Equal(t, ScriptLanguageJavaScript, result.Language,
		"closer package.json should win over farther requirements.txt")
}

// writeFile is a test helper that creates a file with the given content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

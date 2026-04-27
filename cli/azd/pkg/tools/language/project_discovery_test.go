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
	assert.Equal(t, HookKindPython, result.Language)
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
	assert.Equal(t, HookKindPython, result.Language)
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
	assert.Equal(t, HookKindJavaScript, result.Language)
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
	assert.Equal(t, HookKindDotNet, result.Language)
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
	assert.Equal(t, HookKindDotNet, result.Language)
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
	assert.Equal(t, HookKindPython, result.Language)
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
	assert.Equal(t, HookKindPython, result.Language,
		"requirements.txt has higher priority than package.json")
	assert.Equal(t, reqFile, result.DependencyFile)
}

func TestDiscoverProjectFile_PyprojectOverRequirements(t *testing.T) {
	// When both pyproject.toml and requirements.txt exist in the
	// same directory, pyproject.toml wins — matching the convention
	// in framework_service_python.go and internal/appdetect/python.go.
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "hook.py")

	pyprojectFile := filepath.Join(dir, "pyproject.toml")
	writeFile(t, pyprojectFile, "[project]\nname = \"demo\"\n")
	writeFile(t, filepath.Join(dir, "requirements.txt"), "flask\n")

	result, err := DiscoverProjectFile(scriptPath, dir)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, HookKindPython, result.Language)
	assert.Equal(t, pyprojectFile, result.DependencyFile,
		"pyproject.toml should win over requirements.txt (PEP 621)")
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
	assert.Equal(t, HookKindJavaScript, result.Language)
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
	assert.Equal(t, HookKindJavaScript, result.Language,
		"closer package.json should win over farther requirements.txt")
}

// ---------------------------------------------------------------------------
// DiscoverNodeProject tests
// ---------------------------------------------------------------------------

func TestDiscoverNodeProject_FindsPackageJson(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "hook.js")
	projectFile := filepath.Join(dir, "package.json")

	writeFile(t, projectFile, "{}\n")

	result, err := DiscoverNodeProject(scriptPath, dir)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, dir, result.ProjectDir)
	assert.Equal(t, projectFile, result.DependencyFile)
	assert.Equal(t, HookKindJavaScript, result.Language)
}

func TestDiscoverNodeProject_IgnoresPythonFiles(t *testing.T) {
	// When both requirements.txt and package.json exist,
	// DiscoverNodeProject must return package.json.
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "hook.js")
	packageFile := filepath.Join(dir, "package.json")

	writeFile(t, filepath.Join(dir, "requirements.txt"), "flask\n")
	writeFile(t, filepath.Join(dir, "pyproject.toml"), "[project]\n")
	writeFile(t, packageFile, "{}\n")

	result, err := DiscoverNodeProject(scriptPath, dir)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, packageFile, result.DependencyFile,
		"should find package.json even when Python project files exist")
	assert.Equal(t, HookKindJavaScript, result.Language)
}

func TestDiscoverNodeProject_RespectsUpperBoundary(t *testing.T) {
	// package.json above the boundary must not be found.
	//
	// root/
	//   package.json   <- above boundary
	//   child/         <- boundary
	//     hook.js
	root := t.TempDir()
	child := filepath.Join(root, "child")
	require.NoError(t, os.MkdirAll(child, 0o700))

	writeFile(t, filepath.Join(root, "package.json"), "{}\n")

	scriptPath := filepath.Join(child, "hook.js")

	result, err := DiscoverNodeProject(scriptPath, child)

	require.NoError(t, err)
	assert.Nil(t, result,
		"package.json above boundary must not be found")
}

func TestDiscoverNodeProject_WalksUpToPackageJson(t *testing.T) {
	// package.json in parent, script in child subdirectory.
	root := t.TempDir()
	hooksDir := filepath.Join(root, "hooks")
	require.NoError(t, os.MkdirAll(hooksDir, 0o700))

	projectFile := filepath.Join(root, "package.json")
	writeFile(t, projectFile, "{}\n")

	scriptPath := filepath.Join(hooksDir, "hook.js")

	result, err := DiscoverNodeProject(scriptPath, root)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, root, result.ProjectDir)
	assert.Equal(t, projectFile, result.DependencyFile)
}

func TestDiscoverNodeProject_NoPackageJson(t *testing.T) {
	// Directory with only Python files — no package.json.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "requirements.txt"), "flask\n")

	scriptPath := filepath.Join(dir, "hook.js")

	result, err := DiscoverNodeProject(scriptPath, dir)

	require.NoError(t, err)
	assert.Nil(t, result,
		"expected nil when no package.json exists")
}

// writeFile is a test helper that creates a file with the given content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

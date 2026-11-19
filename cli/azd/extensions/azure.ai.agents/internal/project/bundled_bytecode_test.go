// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"archive/zip"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeBundledPythonTree lays out a source directory the way bundled mode expects:
// dependencies installed with pip --target next to the agent code, bytecode
// precompiled with compileall, and the .agentignore that `azd ai agent init` writes.
func writeBundledPythonTree(t *testing.T, srcDir string) {
	t.Helper()
	files := map[string]string{
		"main.py":                          "import requests\n",
		"requirements.txt":                 "requests\n",
		"__pycache__/main.cpython-313.pyc": "bytecode",
		"requests/__init__.py":             "",
		"requests/__pycache__/__init__.cpython-313.pyc": "bytecode",
		"requests-2.32.0.dist-info/METADATA":            "Name: requests\n",
		".venv/lib/__pycache__/site.cpython-313.pyc":    "bytecode",
		".agentignore": DefaultAgentIgnoreContent(),
	}
	for name, content := range files {
		path := filepath.Join(srcDir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
}

func zipEntryNames(t *testing.T, zipPath string) []string {
	t.Helper()
	archive, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = archive.Close() })
	names := make([]string, 0, len(archive.File))
	for _, file := range archive.File {
		names = append(names, file.Name)
	}
	slices.Sort(names)
	return names
}

func packageCodeAgent(t *testing.T, runtime, dependencyResolution string) []string {
	t.Helper()
	root := t.TempDir()
	srcDir := filepath.Join(root, "src", "agent")
	require.NoError(t, os.MkdirAll(srcDir, 0o700))
	writeBundledPythonTree(t, srcDir)

	codeConfiguration := map[string]any{"runtime": runtime, "entryPoint": "main.py"}
	if dependencyResolution != "" {
		codeConfiguration["dependencyResolution"] = dependencyResolution
	}
	service := validationTestService(t, root, "inline", map[string]any{
		"kind":              "hosted",
		"name":              "bundled-agent",
		"codeConfiguration": codeConfiguration,
	})
	provider := &AgentServiceTargetProvider{
		projectPath:   root,
		servicePath:   srcDir,
		serviceConfig: service,
	}

	zipPath, _, err := provider.packageCodeDeploy(t.Context(), service)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(zipPath) })
	return zipEntryNames(t, zipPath)
}

func TestPackageCodeDeploy_PythonBundledKeepsBytecode(t *testing.T) {
	names := packageCodeAgent(t, "python_3_13", "bundled")

	require.Equal(t, []string{
		"__pycache__/main.cpython-313.pyc",
		"main.py",
		"requests-2.32.0.dist-info/METADATA",
		"requests/__init__.py",
		"requests/__pycache__/__init__.cpython-313.pyc",
		"requirements.txt",
	}, names)
}

func TestPackageCodeDeploy_RemoteBuildStillDropsBytecode(t *testing.T) {
	for _, dependencyResolution := range []string{"", "remote_build"} {
		t.Run("dependencyResolution="+dependencyResolution, func(t *testing.T) {
			names := packageCodeAgent(t, "python_3_13", dependencyResolution)

			require.Equal(t, []string{
				"main.py",
				"requests-2.32.0.dist-info/METADATA",
				"requests/__init__.py",
				"requirements.txt",
			}, names)
		})
	}
}

func TestAgentIgnore_KeepPythonBytecode(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, ".agentignore"), []byte(DefaultAgentIgnoreContent()), 0o600))

	m, err := newAgentIgnoreMatcher(t.Context(), dir)
	require.NoError(t, err)
	m.keepPythonBytecode = true

	require.False(t, m.ShouldExclude("__pycache__", true))
	require.False(t, m.ShouldExclude("pkg/__pycache__", true))
	require.False(t, m.ShouldExclude("pkg/__pycache__/mod.cpython-313.pyc", false))
	require.False(t, m.ShouldExclude("legacy.pyc", false))

	// Everything else in .agentignore still applies.
	require.True(t, m.ShouldExclude(".venv", true))
	require.True(t, m.ShouldExclude("legacy.pyo", false))
	require.True(t, m.ShouldExclude(".env", false))
	require.True(t, m.ShouldExclude("agent.yaml", false))
}

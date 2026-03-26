// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dotnet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveEntryPoint_DirectCsFile(t *testing.T) {
	tempDir := t.TempDir()

	csPath := filepath.Join(tempDir, "infra.cs")
	err := os.WriteFile(csPath, []byte("Console.WriteLine(\"hello\");"), 0600)
	require.NoError(t, err)

	provider := &DotNetProvider{}
	result, err := provider.resolveEntryPoint(csPath)
	require.NoError(t, err)
	require.Equal(t, csPath, result)
}

func TestResolveEntryPoint_DirectoryWithSingleCsFile(t *testing.T) {
	tempDir := t.TempDir()

	csPath := filepath.Join(tempDir, "infra.cs")
	err := os.WriteFile(csPath, []byte("Console.WriteLine(\"hello\");"), 0600)
	require.NoError(t, err)

	provider := &DotNetProvider{}
	result, err := provider.resolveEntryPoint(tempDir)
	require.NoError(t, err)
	require.Equal(t, csPath, result)
}

func TestResolveEntryPoint_DirectoryWithCsproj(t *testing.T) {
	tempDir := t.TempDir()

	csprojPath := filepath.Join(tempDir, "Infra.csproj")
	err := os.WriteFile(csprojPath, []byte("<Project/>"), 0600)
	require.NoError(t, err)
	// Create multiple .cs files alongside - forces fallback to project directory
	err = os.WriteFile(filepath.Join(tempDir, "Program.cs"), []byte("// main"), 0600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tempDir, "Resources.cs"), []byte("// resources"), 0600)
	require.NoError(t, err)

	provider := &DotNetProvider{}
	result, err := provider.resolveEntryPoint(tempDir)
	require.NoError(t, err)
	// With multiple .cs files and a csproj, the directory is returned for project-based run
	require.Equal(t, tempDir, result)
}

func TestResolveEntryPoint_DirectoryWithMultipleCsFilesNoCsproj(t *testing.T) {
	tempDir := t.TempDir()

	err := os.WriteFile(filepath.Join(tempDir, "a.cs"), []byte("//a"), 0600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tempDir, "b.cs"), []byte("//b"), 0600)
	require.NoError(t, err)

	provider := &DotNetProvider{}
	_, err = provider.resolveEntryPoint(tempDir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "multiple .cs files")
}

func TestResolveEntryPoint_EmptyDirectory(t *testing.T) {
	tempDir := t.TempDir()

	provider := &DotNetProvider{}
	_, err := provider.resolveEntryPoint(tempDir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no .cs or .NET project file found")
}

func TestResolveEntryPoint_InvalidFileExtension(t *testing.T) {
	tempDir := t.TempDir()

	txtPath := filepath.Join(tempDir, "readme.txt")
	err := os.WriteFile(txtPath, []byte("hello"), 0600)
	require.NoError(t, err)

	provider := &DotNetProvider{}
	_, err = provider.resolveEntryPoint(txtPath)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a valid .NET infrastructure file")
}

func TestResolveEntryPoint_PathDoesNotExist(t *testing.T) {
	provider := &DotNetProvider{}
	_, err := provider.resolveEntryPoint("/nonexistent/path")
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exist")
}

func TestResolveEntryPoint_DirectCsprojFile(t *testing.T) {
	tempDir := t.TempDir()

	csprojPath := filepath.Join(tempDir, "Infra.csproj")
	err := os.WriteFile(csprojPath, []byte("<Project/>"), 0600)
	require.NoError(t, err)

	provider := &DotNetProvider{}
	result, err := provider.resolveEntryPoint(csprojPath)
	require.NoError(t, err)
	require.Equal(t, csprojPath, result)
}

func TestFileNames(t *testing.T) {
	paths := []string{
		filepath.Join("a", "b", "main.bicep"),
		filepath.Join("c", "d", "resources.bicep"),
	}

	names := fileNames(paths)
	require.Equal(t, []string{"main.bicep", "resources.bicep"}, names)
}

func TestFileNames_Empty(t *testing.T) {
	names := fileNames([]string{})
	require.Empty(t, names)
}

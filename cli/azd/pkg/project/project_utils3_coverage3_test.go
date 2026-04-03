// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package project

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_createDeployableZip_AzureDirExcluded_Coverage3(t *testing.T) {
	tmpDir := t.TempDir()
	// Create .azure directory (should be excluded)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".azure"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".azure", "config.json"), []byte("{}"), 0o600))
	// Create a normal file (should be included)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "app.py"), []byte("print('hi')"), 0o600))

	prj := &ProjectConfig{Name: "proj"}
	sc := &ServiceConfig{
		Name:     "web",
		Host:     AppServiceTarget,
		Language: ServiceLanguagePython,
		Project:  prj,
	}

	zipPath, err := createDeployableZip(sc, tmpDir)
	require.NoError(t, err)
	defer os.Remove(zipPath)

	entries := zipEntryNames(t, zipPath)
	assert.Contains(t, entries, "app.py")
	assert.NotContains(t, entries, ".azure/config.json")
	assert.NotContains(t, entries, ".azure/")
}

func Test_createDeployableZip_RemoteBuildFalse_Coverage3(t *testing.T) {
	tmpDir := t.TempDir()
	// Create node_modules directory
	require.NoError(t, os.MkdirAll(
		filepath.Join(tmpDir, "node_modules", "express"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "node_modules", "express", "index.js"), []byte("module.exports={}"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "index.js"), []byte("require('express')"), 0o600))

	remoteBuildFalse := false
	prj := &ProjectConfig{Name: "proj"}
	sc := &ServiceConfig{
		Name:        "web",
		Host:        AppServiceTarget,
		Language:    ServiceLanguageJavaScript,
		Project:     prj,
		RemoteBuild: &remoteBuildFalse,
	}

	zipPath, err := createDeployableZip(sc, tmpDir)
	require.NoError(t, err)
	defer os.Remove(zipPath)

	entries := zipEntryNames(t, zipPath)
	assert.Contains(t, entries, "index.js")
	// With RemoteBuild=false, node_modules should be INCLUDED
	assert.Contains(t, entries, "node_modules/express/index.js")
}

func Test_createDeployableZip_IgnoreFileExcluded_Coverage3(t *testing.T) {
	tmpDir := t.TempDir()
	// AppServiceTarget uses ".deployment" as ignore file; FunctionApp uses ".funcignore"
	// Let's use FunctionApp and a .funcignore file
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".funcignore"), []byte("*.log\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "app.py"), []byte("print('hi')"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "debug.log"), []byte("log data"), 0o600))

	prj := &ProjectConfig{Name: "proj"}
	sc := &ServiceConfig{
		Name:     "func",
		Host:     AzureFunctionTarget,
		Language: ServiceLanguagePython,
		Project:  prj,
	}

	zipPath, err := createDeployableZip(sc, tmpDir)
	require.NoError(t, err)
	defer os.Remove(zipPath)

	entries := zipEntryNames(t, zipPath)
	assert.Contains(t, entries, "app.py")
	// The .funcignore file itself should be excluded
	assert.NotContains(t, entries, ".funcignore")
	// .log files should be excluded by the ignorer
	assert.NotContains(t, entries, "debug.log")
}

func Test_createDeployableZip_WebAppIgnore_Coverage3(t *testing.T) {
	tmpDir := t.TempDir()
	// AppServiceTarget.IgnoreFile() returns ".webappignore"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".webappignore"), []byte("*.tmp\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "index.html"), []byte("<html></html>"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "temp.tmp"), []byte("temp"), 0o600))

	prj := &ProjectConfig{Name: "proj"}
	sc := &ServiceConfig{
		Name:     "web",
		Host:     AppServiceTarget,
		Language: ServiceLanguageJavaScript,
		Project:  prj,
	}

	zipPath, err := createDeployableZip(sc, tmpDir)
	require.NoError(t, err)
	defer os.Remove(zipPath)

	entries := zipEntryNames(t, zipPath)
	assert.Contains(t, entries, "index.html")
	// .tmp files should be excluded by the webappignore
	assert.NotContains(t, entries, "temp.tmp")
	// The .webappignore file itself should be excluded
	assert.NotContains(t, entries, ".webappignore")
}

// zipEntryNames returns all file names in a zip archive.
func zipEntryNames(t *testing.T, zipPath string) []string {
	t.Helper()
	r, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer r.Close()

	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	return names
}

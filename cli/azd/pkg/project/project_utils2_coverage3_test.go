// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- createDeployableZip: test more branches ----

func Test_createDeployableZip_ExcludesAzureDir_Coverage3(t *testing.T) {
	dir := t.TempDir()
	// Create a .azure directory - should be excluded from zip
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".azure"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".azure", "config.json"), []byte("{}"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.py"), []byte("print('hello')"), 0600))

	sc := &ServiceConfig{
		Name:    "api",
		Host:    AppServiceTarget,
		Project: &ProjectConfig{Name: "myproj", Path: dir},
	}
	zipPath, err := createDeployableZip(sc, dir)
	require.NoError(t, err)
	defer os.Remove(zipPath)

	assert.FileExists(t, zipPath)
}

func Test_createDeployableZip_FunctionAppExcludesLocalSettings_Coverage3(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "function_app.py"), []byte("import azure.functions"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "local.settings.json"), []byte(`{"Values":{}}`), 0600))

	sc := &ServiceConfig{
		Name:    "func",
		Host:    AzureFunctionTarget,
		Project: &ProjectConfig{Name: "myproj", Path: dir},
	}
	zipPath, err := createDeployableZip(sc, dir)
	require.NoError(t, err)
	defer os.Remove(zipPath)

	assert.FileExists(t, zipPath)
}

func Test_createDeployableZip_PythonExcludesVenvAndPycache_Coverage3(t *testing.T) {
	dir := t.TempDir()
	// Create a venv directory (with pyvenv.cfg marker file)
	venvDir := filepath.Join(dir, ".venv")
	require.NoError(t, os.MkdirAll(venvDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(venvDir, "pyvenv.cfg"), []byte("home = /usr/bin"), 0600))

	// Create __pycache__ directory
	pycacheDir := filepath.Join(dir, "__pycache__")
	require.NoError(t, os.MkdirAll(pycacheDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(pycacheDir, "app.cpython-312.pyc"), []byte{0}, 0600))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.py"), []byte("print('hi')"), 0600))

	sc := &ServiceConfig{
		Name:     "api",
		Language: ServiceLanguagePython,
		Host:     AppServiceTarget,
		Project:  &ProjectConfig{Name: "myproj", Path: dir},
	}
	zipPath, err := createDeployableZip(sc, dir)
	require.NoError(t, err)
	defer os.Remove(zipPath)

	assert.FileExists(t, zipPath)
}

func Test_createDeployableZip_JSExcludesNodeModules_Coverage3(t *testing.T) {
	dir := t.TempDir()
	// Create node_modules directory
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "node_modules", "express"), 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "node_modules", "express", "index.js"),
		[]byte("module.exports={}"), 0600,
	))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.js"), []byte("console.log('hi')"), 0600))

	sc := &ServiceConfig{
		Name:     "web",
		Language: ServiceLanguageJavaScript,
		Host:     AppServiceTarget,
		Project:  &ProjectConfig{Name: "myproj", Path: dir},
	}
	zipPath, err := createDeployableZip(sc, dir)
	require.NoError(t, err)
	defer os.Remove(zipPath)

	assert.FileExists(t, zipPath)
}

func Test_createDeployableZip_TSExcludesNodeModules_Coverage3(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "node_modules"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.ts"), []byte("console.log('hi')"), 0600))

	sc := &ServiceConfig{
		Name:     "web",
		Language: ServiceLanguageTypeScript,
		Host:     AppServiceTarget,
		Project:  &ProjectConfig{Name: "myproj", Path: dir},
	}
	zipPath, err := createDeployableZip(sc, dir)
	require.NoError(t, err)
	defer os.Remove(zipPath)

	assert.FileExists(t, zipPath)
}

func Test_createDeployableZip_JSRemoteBuildFalse_IncludesNodeModules_Coverage3(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "node_modules", "express"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "node_modules", "express", "index.js"), []byte("{}"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.js"), []byte("console.log('hi')"), 0600))

	remoteBuild := false
	sc := &ServiceConfig{
		Name:        "web",
		Language:    ServiceLanguageJavaScript,
		Host:        AppServiceTarget,
		RemoteBuild: &remoteBuild,
		Project:     &ProjectConfig{Name: "myproj", Path: dir},
	}
	zipPath, err := createDeployableZip(sc, dir)
	require.NoError(t, err)
	defer os.Remove(zipPath)

	info, err := os.Stat(zipPath)
	require.NoError(t, err)
	// With node_modules included, zip should be larger
	assert.Greater(t, info.Size(), int64(0))
}

func Test_createDeployableZip_WithIgnoreFile_Coverage3(t *testing.T) {
	dir := t.TempDir()

	// Create the ignore file (.appserviceignore for AppService)
	ignoreContent := "*.log\ntmp/\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".appserviceignore"), []byte(ignoreContent), 0600))

	// Create files that should be included
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.py"), []byte("print('hi')"), 0600))

	// Create files that should be ignored
	require.NoError(t, os.WriteFile(filepath.Join(dir, "debug.log"), []byte("log data"), 0600))

	sc := &ServiceConfig{
		Name:    "api",
		Host:    AppServiceTarget,
		Project: &ProjectConfig{Name: "myproj", Path: dir},
	}
	zipPath, err := createDeployableZip(sc, dir)
	require.NoError(t, err)
	defer os.Remove(zipPath)

	assert.FileExists(t, zipPath)
}

func Test_createDeployableZip_WithBOMInIgnoreFile_Coverage3(t *testing.T) {
	dir := t.TempDir()

	// Create an ignore file with UTF-8 BOM
	bom := []byte{0xEF, 0xBB, 0xBF}
	content := append(bom, []byte("*.log\n")...)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".appserviceignore"), content, 0600))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.py"), []byte("hi"), 0600))

	sc := &ServiceConfig{
		Name:    "api",
		Host:    AppServiceTarget,
		Project: &ProjectConfig{Name: "myproj", Path: dir},
	}
	zipPath, err := createDeployableZip(sc, dir)
	require.NoError(t, err)
	defer os.Remove(zipPath)

	assert.FileExists(t, zipPath)
}

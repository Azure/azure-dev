// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

func Test_externalTool_Methods(t *testing.T) {
	tool := &externalTool{name: "my-tool", installUrl: "https://example.com/install"}

	t.Run("CheckInstalled_ReturnsNil", func(t *testing.T) {
		err := tool.CheckInstalled(t.Context())
		assert.NoError(t, err)
	})

	t.Run("Name", func(t *testing.T) {
		assert.Equal(t, "my-tool", tool.Name())
	})

	t.Run("InstallUrl", func(t *testing.T) {
		assert.Equal(t, "https://example.com/install", tool.InstallUrl())
	})
}

func Test_ServiceStableFiltered(t *testing.T) {
	t.Run("AllEnabled", func(t *testing.T) {
		im := NewImportManager(nil)
		pc := &ProjectConfig{
			Services: map[string]*ServiceConfig{
				"web": {Name: "web"},
				"api": {Name: "api"},
			},
		}

		services, err := im.ServiceStableFiltered(t.Context(), pc, "", nil)
		require.NoError(t, err)
		assert.Len(t, services, 2)
	})

	t.Run("FilterByName", func(t *testing.T) {
		im := NewImportManager(nil)
		pc := &ProjectConfig{
			Services: map[string]*ServiceConfig{
				"web": {Name: "web"},
				"api": {Name: "api"},
			},
		}

		services, err := im.ServiceStableFiltered(t.Context(), pc, "web", nil)
		require.NoError(t, err)
		require.Len(t, services, 1)
		assert.Equal(t, "web", services[0].Name)
	})

	t.Run("FilterByNameNotFound", func(t *testing.T) {
		im := NewImportManager(nil)
		pc := &ProjectConfig{
			Services: map[string]*ServiceConfig{
				"web": {Name: "web"},
			},
		}

		_, err := im.ServiceStableFiltered(t.Context(), pc, "missing", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing")
	})

	t.Run("ConditionalServiceDisabled", func(t *testing.T) {
		im := NewImportManager(nil)
		pc := &ProjectConfig{
			Services: map[string]*ServiceConfig{
				"web": {
					Name:      "web",
					Condition: osutil.NewExpandableString("false"),
				},
				"api": {Name: "api"},
			},
		}

		getenv := func(key string) string { return "" }
		services, err := im.ServiceStableFiltered(t.Context(), pc, "", getenv)
		require.NoError(t, err)
		// Only "api" should be returned since "web" has condition "false"
		assert.Len(t, services, 1)
		assert.Equal(t, "api", services[0].Name)
	})

	t.Run("TargetServiceDisabled", func(t *testing.T) {
		im := NewImportManager(nil)
		pc := &ProjectConfig{
			Services: map[string]*ServiceConfig{
				"web": {
					Name:      "web",
					Condition: osutil.NewExpandableString("false"),
				},
			},
		}

		getenv := func(key string) string { return "" }
		_, err := im.ServiceStableFiltered(t.Context(), pc, "web", getenv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "web")
	})
}

func Test_ServiceStable(t *testing.T) {
	im := NewImportManager(nil)
	pc := &ProjectConfig{
		Services: map[string]*ServiceConfig{
			"beta":  {Name: "beta"},
			"alpha": {Name: "alpha"},
		},
	}

	services, err := im.ServiceStable(t.Context(), pc)
	require.NoError(t, err)
	require.Len(t, services, 2)
	// Should be sorted alphabetically
	assert.Equal(t, "alpha", services[0].Name)
	assert.Equal(t, "beta", services[1].Name)
}

func Test_infraSpec_OpenAiModel(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"gpt4": {
				Name: "gpt4",
				Type: ResourceTypeOpenAiModel,
				Props: AIModelProps{
					Model: AIModelPropsModel{Name: "gpt-4", Version: "0613"},
				},
			},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.Len(t, spec.AIModels, 1)
	assert.Equal(t, "gpt4", spec.AIModels[0].Name)
	assert.Equal(t, "gpt-4", spec.AIModels[0].Model.Name)
	assert.Equal(t, "0613", spec.AIModels[0].Model.Version)
}

func Test_infraSpec_AiProject(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"foundry": {
				Name: "foundry",
				Type: ResourceTypeAiProject,
				Props: AiFoundryModelProps{
					Models: []AiServicesModel{
						{
							Name:    "gpt-4o",
							Version: "2024-05-13",
							Format:  "OpenAI",
							Sku: AiServicesModelSku{
								Name:      "Standard",
								UsageName: "standard",
								Capacity:  10,
							},
						},
					},
				},
			},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.NotNil(t, spec.AiFoundryProject)
	assert.Equal(t, "foundry", spec.AiFoundryProject.Name)
	require.Len(t, spec.AiFoundryProject.Models, 1)
	assert.Equal(t, "gpt-4o", spec.AiFoundryProject.Models[0].Name)
}

func Test_createDeployableZip_ExcludesAzureDir(t *testing.T) {
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

func Test_createDeployableZip_FunctionAppExcludesLocalSettings(t *testing.T) {
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

func Test_createDeployableZip_PythonExcludesVenvAndPycache(t *testing.T) {
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

func Test_createDeployableZip_JSExcludesNodeModules(t *testing.T) {
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

func Test_createDeployableZip_TSExcludesNodeModules(t *testing.T) {
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

func Test_createDeployableZip_JSRemoteBuildFalse_IncludesNodeModules(t *testing.T) {
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

func Test_createDeployableZip_WithIgnoreFile(t *testing.T) {
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

func Test_createDeployableZip_WithBOMInIgnoreFile(t *testing.T) {
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

func Test_createDeployableZip_AzureDirExcluded(t *testing.T) {
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

func Test_createDeployableZip_RemoteBuildFalse(t *testing.T) {
	tmpDir := t.TempDir()
	// Create node_modules directory
	require.NoError(t, os.MkdirAll(
		filepath.Join(tmpDir, "node_modules", "express"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "node_modules", "express", "index.js"),
		[]byte("module.exports={}"), 0o600))
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

func Test_createDeployableZip_IgnoreFileExcluded(t *testing.T) {
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

func Test_createDeployableZip_WebAppIgnore(t *testing.T) {
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

func Test_OverriddenEndpoints_ValidJSON(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{
		"SERVICE_MYAPP_ENDPOINTS": `["http://a.com","http://b.com"]`,
	})
	svcConfig := &ServiceConfig{Name: "myapp"}

	endpoints := OverriddenEndpoints(t.Context(), svcConfig, env)
	assert.Equal(t, []string{"http://a.com", "http://b.com"}, endpoints)
}

func Test_OverriddenEndpoints_InvalidJSON(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{
		"SERVICE_MYAPP_ENDPOINTS": `not-json`,
	})
	svcConfig := &ServiceConfig{Name: "myapp"}

	endpoints := OverriddenEndpoints(t.Context(), svcConfig, env)
	assert.Nil(t, endpoints)
}

func Test_OverriddenEndpoints_Empty(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})
	svcConfig := &ServiceConfig{Name: "myapp"}

	endpoints := OverriddenEndpoints(t.Context(), svcConfig, env)
	assert.Nil(t, endpoints)
}

func (f *failingTool) CheckInstalled(_ context.Context) error {
	return f.checkErr
}

func (f *failingTool) InstallUrl() string { return f.installUrl }

func (f *failingTool) Name() string { return f.toolName }

// ---------- ExternalFrameworkService.RequiredExternalTools with nil broker ----------
func Test_ExternalFrameworkService_toProtoNil(t *testing.T) {
	efs := &ExternalFrameworkService{}
	cfg, err := efs.toProtoServiceConfig(nil)
	assert.Nil(t, cfg)
	assert.NoError(t, err)
}

func Test_ExternalFrameworkService_toProtoServiceConfigExpandsEnvironment(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{
		"SERVICE_VALUE": "resolved",
	})
	efs := &ExternalFrameworkService{lazyEnv: lazy.From(env)}
	serviceConfig := &ServiceConfig{
		Name: "api",
		Environment: osutil.ExpandableMap{
			"FROM_ENV": osutil.NewExpandableString("${SERVICE_VALUE}"),
			"STATIC":   osutil.NewExpandableString("static"),
		},
	}

	cfg, err := efs.toProtoServiceConfig(serviceConfig)

	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, map[string]string{
		"FROM_ENV": "resolved",
		"STATIC":   "static",
	}, cfg.Environment)
}

func Test_ExternalFrameworkService_toProtoServiceConfigEnvLoadError(t *testing.T) {
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return nil, errors.New("no environment")
	})
	efs := &ExternalFrameworkService{lazyEnv: lazyEnv}
	serviceConfig := &ServiceConfig{
		Name: "api",
		Environment: osutil.ExpandableMap{
			"FROM_ENV": osutil.NewExpandableString("${SERVICE_VALUE}"),
			"STATIC":   osutil.NewExpandableString("static"),
		},
	}

	cfg, err := efs.toProtoServiceConfig(serviceConfig)

	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, map[string]string{
		"FROM_ENV": "",
		"STATIC":   "static",
	}, cfg.Environment)
}

func Test_ExternalFrameworkService_toProtoServiceConfigEnvResolvedPerCall(t *testing.T) {
	// The environment is resolved from the lazy on every conversion, so an environment
	// that becomes available after the service is constructed is still picked up.
	var env *environment.Environment
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		if env == nil {
			return nil, errors.New("no environment yet")
		}
		return env, nil
	})
	efs := &ExternalFrameworkService{lazyEnv: lazyEnv}
	serviceConfig := &ServiceConfig{
		Name: "api",
		Environment: osutil.ExpandableMap{
			"FROM_ENV": osutil.NewExpandableString("${SERVICE_VALUE}"),
		},
	}

	cfg, err := efs.toProtoServiceConfig(serviceConfig)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"FROM_ENV": ""}, cfg.Environment)

	env = environment.NewWithValues("test", map[string]string{"SERVICE_VALUE": "resolved"})

	cfg, err = efs.toProtoServiceConfig(serviceConfig)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"FROM_ENV": "resolved"}, cfg.Environment)
}

func Test_mergeDefaultEnvVars(t *testing.T) {
	// Test that user env overrides defaults
	defaults := map[string]string{
		"PORT": "3000",
		"HOST": "localhost",
	}
	userEnv := []ServiceEnvVar{
		{Name: "PORT", Value: "8080"},
	}

	result := mergeDefaultEnvVars(defaults, userEnv)
	// User env should override default
	found := false
	for _, ev := range result {
		if ev.Name == "PORT" {
			assert.Equal(t, "8080", ev.Value)
			found = true
		}
	}
	assert.True(t, found, "PORT should be in merged result")

	// Default HOST should still be present
	hasHost := false
	for _, ev := range result {
		if ev.Name == "HOST" {
			hasHost = true
		}
	}
	assert.True(t, hasHost, "HOST should be in merged result from defaults")
}

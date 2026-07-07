// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
)

func TestMapToStringSlice(t *testing.T) {
	// Test case 1: Empty map
	m1 := make(map[string]string)
	expected1 := []string(nil)
	result1 := mapToStringSlice(m1, ":")
	assert.ElementsMatch(t, expected1, result1)

	// Test case 2: Map with values
	m2 := map[string]string{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	}
	expected2 := []string{"key1:value1", "key2:value2", "key3:value3"}
	result2 := mapToStringSlice(m2, ":")
	assert.ElementsMatch(t, expected2, result2)

	// Test case 3: Map with empty values
	m3 := map[string]string{
		"key1": "",
		"key2": "",
		"key3": "",
	}
	expected3 := []string{"key1", "key2", "key3"}
	result3 := mapToStringSlice(m3, ":")
	assert.ElementsMatch(t, expected3, result3)
}

func TestEvaluateArgsWithConfig(t *testing.T) {
	envParamName := "param4"
	envParamKey := strings.TrimSuffix(scaffold.EnvFormat(envParamName)[2:], "}")
	envParamExpected := "valueFromEnv"
	t.Setenv(envParamKey, envParamExpected)

	manifest := apphost.Manifest{
		Resources: map[string]*apphost.Resource{
			"param1": {
				Type:  "parameter.v0",
				Value: "value1",
			},
			"param2": {
				Type:  "parameter.v0",
				Value: "value2",
			},
			"param3": {
				Type:  "parameter.v0",
				Value: "{param3.inputs.iParam}",
				Inputs: map[string]apphost.Input{
					"iParam": {
						Type: "string",
					},
				},
			},
			envParamName: {
				Type:  "parameter.v0",
				Value: fmt.Sprintf("{%s.inputs.foo}", envParamName),
				Inputs: map[string]apphost.Input{
					"foo": {
						Type: "string",
					},
				},
			},
		},
	}

	args := map[string]string{
		"arg1": "{param1.value}",
		"arg2": "{param2.value}",
		"arg3": "constant",
		"arg4": "{param3.value}",
		"arg5": "{param4.value}",
	}

	expected := map[string]string{
		// evaluation completed
		"arg1": "value1",
		"arg2": "value2",
		// constant value
		"arg3": "constant",
		// evaluation delayed until building container
		"arg4": "{infra.parameters.param3}",
		// evaluation from environment variable
		"arg5": envParamExpected,
	}

	result, err := evaluateBuildArgs(manifest, args)
	require.NoError(t, err)
	require.ElementsMatch(t, mapToStringSlice(expected, ","), mapToStringSlice(result, ","))
}

func TestBuildArgsArrayAndEnv(t *testing.T) {
	manifest := apphost.Manifest{
		Resources: map[string]*apphost.Resource{
			"param1": {
				Type:  "parameter.v0",
				Value: "value1",
			},
			"param2": {
				Type:  "parameter.v0",
				Value: "value2",
			},
		},
	}

	bArgs := map[string]apphost.ContainerV1BuildSecrets{
		"arg1": {
			Type:  "env",
			Value: new("{param1.value}"),
		},
		"arg2": {
			Type:   "file",
			Source: new("/path/to/secret"),
		},
	}

	expectedArgs := []string{
		"id=arg1",
		"id=arg2,src=/path/to/secret",
	}

	expectedEnv := []string{
		"arg1=value1",
	}

	args, env, err := buildArgsArrayAndEnv(manifest, bArgs)
	require.NoError(t, err)
	assert.ElementsMatch(t, expectedArgs, args)
	assert.ElementsMatch(t, expectedEnv, env)
}

func Test_NewDotNetImporter(t *testing.T) {
	imp := NewDotNetImporter(nil, nil, nil, nil, nil)
	require.NotNil(t, imp)
	// Verify cache maps initialized
	assert.NotNil(t, imp.cache)
	assert.NotNil(t, imp.hostCheck)
}

func Test_infraSpec_HostContainerApp(t *testing.T) {
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"api": {
				Name:  "api",
				Type:  ResourceTypeHostContainerApp,
				Props: ContainerAppProps{Port: 8080},
			},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.Len(t, spec.Services, 1)
	assert.Equal(t, "api", spec.Services[0].Name)
	assert.Equal(t, 8080, spec.Services[0].Port)
	assert.Equal(t, scaffold.ContainerAppKind, spec.Services[0].Host)
}

func Test_infraSpec_HostContainerApp_WithDeps(t *testing.T) {
	// Tests backend-frontend mapping reverse pass
	prj := &ProjectConfig{
		Resources: map[string]*ResourceConfig{
			"api": {
				Name:  "api",
				Type:  ResourceTypeHostContainerApp,
				Props: ContainerAppProps{Port: 3000},
			},
			"web": {
				Name:  "web",
				Type:  ResourceTypeHostContainerApp,
				Props: ContainerAppProps{Port: 8080},
				Uses:  []string{"api"},
			},
		},
	}
	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.Len(t, spec.Services, 2)

	// Services are sorted by name
	assert.Equal(t, "api", spec.Services[0].Name)
	assert.Equal(t, "web", spec.Services[1].Name)

	// api should have a Backend pointing to frontend "web"
	require.NotNil(t, spec.Services[0].Backend)
	require.Len(t, spec.Services[0].Backend.Frontends, 1)
	assert.Equal(t, "web", spec.Services[0].Backend.Frontends[0].Name)

	// web should have a Frontend pointing to backend "api"
	require.NotNil(t, spec.Services[1].Frontend)
	require.Len(t, spec.Services[1].Frontend.Backends, 1)
	assert.Equal(t, "api", spec.Services[1].Frontend.Backends[0].Name)
}

func Test_infraSpec_HostAppService_Valid(t *testing.T) {
	dir := t.TempDir()
	prj := &ProjectConfig{
		Path: dir,
		Services: map[string]*ServiceConfig{
			"webapp": {
				Name:         "webapp",
				Language:     ServiceLanguagePython,
				RelativePath: ".",
				Project:      nil, // will be set below
			},
		},
		Resources: map[string]*ResourceConfig{
			"webapp": {
				Name: "webapp",
				Type: ResourceTypeHostAppService,
				Props: AppServiceProps{
					Port:    8000,
					Runtime: AppServiceRuntime{Stack: "python", Version: "3.12"},
				},
			},
		},
	}
	prj.Services["webapp"].Project = prj

	spec, err := infraSpec(prj)
	require.NoError(t, err)
	require.Len(t, spec.Services, 1)
	assert.Equal(t, "webapp", spec.Services[0].Name)
	assert.Equal(t, scaffold.AppServiceKind, spec.Services[0].Host)
	assert.Equal(t, 8000, spec.Services[0].Port)
}

func Test_HasAppHost_Extended(t *testing.T) {
	t.Run("DotNetService_CanImportTrue", func(t *testing.T) {
		tempDir := t.TempDir()
		dotNetPath := filepath.Join(tempDir, "apphost")
		os.MkdirAll(dotNetPath, 0755)

		importer := NewDotNetImporter(nil, nil, nil, nil, nil)
		// Pre-populate the hostCheck cache so CanImport returns true without needing a real CLI
		importer.hostCheck[dotNetPath] = hostCheckResult{is: true}

		im := NewImportManager(importer)
		prj := &ProjectConfig{
			Path: tempDir,
			Services: map[string]*ServiceConfig{
				"apphost": {
					Name:         "apphost",
					Language:     ServiceLanguageDotNet,
					RelativePath: "apphost",
					Project:      &ProjectConfig{Path: tempDir},
				},
			},
		}
		result := im.HasAppHost(t.Context(), prj)
		assert.True(t, result)
	})

	t.Run("DotNetService_CanImportError", func(t *testing.T) {
		tempDir := t.TempDir()
		importer := NewDotNetImporter(nil, nil, nil, nil, nil)
		importer.hostCheck[tempDir] = hostCheckResult{is: false, err: errors.New("detection failed")}

		im := NewImportManager(importer)
		prj := &ProjectConfig{
			Path: tempDir,
			Services: map[string]*ServiceConfig{
				"apphost": {
					Name:         "apphost",
					Language:     ServiceLanguageDotNet,
					RelativePath: ".",
					Project:      &ProjectConfig{Path: tempDir},
				},
			},
		}
		// Should return false and log the error
		result := im.HasAppHost(t.Context(), prj)
		assert.False(t, result)
	})

	t.Run("DotNetService_CanImportFalse", func(t *testing.T) {
		tempDir := t.TempDir()
		importer := NewDotNetImporter(nil, nil, nil, nil, nil)
		importer.hostCheck[tempDir] = hostCheckResult{is: false}

		im := NewImportManager(importer)
		prj := &ProjectConfig{
			Path: tempDir,
			Services: map[string]*ServiceConfig{
				"apphost": {
					Name:         "apphost",
					Language:     ServiceLanguageDotNet,
					RelativePath: ".",
					Project:      &ProjectConfig{Path: tempDir},
				},
			},
		}
		result := im.HasAppHost(t.Context(), prj)
		assert.False(t, result)
	})
}

// ---------- GenerateAllInfrastructure: DotNet CanImport false ----------
func Test_GenerateAllInfrastructure_DotNet(t *testing.T) {
	t.Run("CanImportFalse_FallsThrough", func(t *testing.T) {
		tmpDir := t.TempDir()
		svcDir := filepath.Join(tmpDir, "api")
		require.NoError(t, os.MkdirAll(svcDir, 0o755))

		dotNetImp := &DotNetImporter{hostCheck: map[string]hostCheckResult{}}
		dotNetImp.hostCheck[svcDir] = hostCheckResult{is: false}
		im := NewImportManager(dotNetImp)

		prj := &ProjectConfig{Path: tmpDir, Services: map[string]*ServiceConfig{}}
		sc := &ServiceConfig{
			Name:         "api",
			RelativePath: "api",
			Language:     ServiceLanguageDotNet,
			Host:         AppServiceTarget,
			Project:      prj,
		}
		prj.Services["api"] = sc

		_, err := im.GenerateAllInfrastructure(t.Context(), prj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not contain any infrastructure")
	})

	t.Run("CanImportError_LogsAndFallsThrough", func(t *testing.T) {
		tmpDir := t.TempDir()
		svcDir := filepath.Join(tmpDir, "api")
		require.NoError(t, os.MkdirAll(svcDir, 0o755))

		dotNetImp := &DotNetImporter{hostCheck: map[string]hostCheckResult{}}
		dotNetImp.hostCheck[svcDir] = hostCheckResult{is: false, err: assert.AnError}
		im := NewImportManager(dotNetImp)

		prj := &ProjectConfig{Path: tmpDir, Services: map[string]*ServiceConfig{}}
		sc := &ServiceConfig{
			Name:         "api",
			RelativePath: "api",
			Language:     ServiceLanguageDotNet,
			Host:         AppServiceTarget,
			Project:      prj,
		}
		prj.Services["api"] = sc

		_, err := im.GenerateAllInfrastructure(t.Context(), prj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not contain any infrastructure")
	})

	t.Run("WithResources_CallsInfraFsForProject", func(t *testing.T) {
		tmpDir := t.TempDir()
		prj := &ProjectConfig{
			Path:     tmpDir,
			Services: map[string]*ServiceConfig{},
			Resources: map[string]*ResourceConfig{
				"db": {
					Type: ResourceTypeDbPostgres,
					Name: "mydb",
				},
			},
		}
		im := NewImportManager(nil)

		result, err := im.GenerateAllInfrastructure(t.Context(), prj)
		// This calls infraFsForProject which generates Bicep from resources
		require.NoError(t, err)
		assert.NotNil(t, result)
	})
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestGetAdditionalProperty(t *testing.T) {
	t.Run("returns empty for nil service config", func(t *testing.T) {
		assert.Equal(t, "", getAdditionalProperty(nil, "customCodeProject"))
	})

	t.Run("returns empty for nil additional properties", func(t *testing.T) {
		svc := &azdext.ServiceConfig{}
		assert.Equal(t, "", getAdditionalProperty(svc, "customCodeProject"))
	})

	t.Run("returns property value when present", func(t *testing.T) {
		svc := newServiceConfig("logicApp", "src/logicApp", map[string]any{
			"customCodeProject": "Functions/Functions.csproj",
		})

		assert.Equal(t, "Functions/Functions.csproj", getAdditionalProperty(svc, "customCodeProject"))
	})
}

func TestHasCustomCodeProjectConfigured(t *testing.T) {
	t.Run("returns false for nil service config", func(t *testing.T) {
		assert.False(t, hasCustomCodeProjectConfigured(nil))
	})

	t.Run("returns false when customCodeProject is absent", func(t *testing.T) {
		assert.False(t, hasCustomCodeProjectConfigured(newServiceConfig("logicApp", "src/logicApp", nil)))
	})

	t.Run("returns true when customCodeProject is present", func(t *testing.T) {
		assert.True(t, hasCustomCodeProjectConfigured(newServiceConfig("logicApp", "src/logicApp", map[string]any{
			"customCodeProject": "Functions/Functions.csproj",
		})))
	})
}

func TestRequiredExternalTools(t *testing.T) {
	provider := &LogicAppsStandardFrameworkServiceProvider{}

	t.Run("returns nil when custom code is not configured", func(t *testing.T) {
		tools, err := provider.RequiredExternalTools(t.Context(), newServiceConfig("logicApp", "src/logicApp", nil))
		require.NoError(t, err)
		assert.Nil(t, tools)
	})

	t.Run("returns dotnet when custom code is configured", func(t *testing.T) {
		tools, err := provider.RequiredExternalTools(
			t.Context(),
			newServiceConfig("logicApp", "src/logicApp", map[string]any{
				"customCodeProject": "Functions/Functions.csproj",
			}))
		require.NoError(t, err)
		require.Len(t, tools, 1)
		assert.Equal(t, "dotnet", tools[0].Name)
		assert.Equal(t, "https://dotnet.microsoft.com/download", tools[0].InstallUrl)
	})
}

func TestRequirements(t *testing.T) {
	t.Run("disables restore and build when service config is not initialized", func(t *testing.T) {
		provider := &LogicAppsStandardFrameworkServiceProvider{}
		reqs, err := provider.Requirements()
		require.NoError(t, err)
		require.NotNil(t, reqs.Package)
		assert.False(t, reqs.Package.RequireRestore)
		assert.False(t, reqs.Package.RequireBuild)
	})

	t.Run("disables restore and build when initialized without customCodeProject", func(t *testing.T) {
		provider := &LogicAppsStandardFrameworkServiceProvider{}
		provider.serviceConfig = newServiceConfig("logicApp", "src/logicApp", nil)
		reqs, err := provider.Requirements()
		require.NoError(t, err)
		require.NotNil(t, reqs.Package)
		assert.False(t, reqs.Package.RequireRestore)
		assert.False(t, reqs.Package.RequireBuild)
	})

	t.Run("enables restore and build when customCodeProject is configured", func(t *testing.T) {
		provider := &LogicAppsStandardFrameworkServiceProvider{}
		provider.serviceConfig = newServiceConfig("logicApp", "src/logicApp", map[string]any{
			"customCodeProject": "Functions/Functions.csproj",
		})
		reqs, err := provider.Requirements()
		require.NoError(t, err)
		require.NotNil(t, reqs.Package)
		assert.True(t, reqs.Package.RequireRestore)
		assert.True(t, reqs.Package.RequireBuild)
	})
}

func TestInitializeValidatesCustomCodeProjectPath(t *testing.T) {
	projectDir := t.TempDir()
	createFile(t, filepath.Join(projectDir, "azure.yaml"), "name: test-project\n")

	t.Run("succeeds without customCodeProject and sets serviceConfig", func(t *testing.T) {
		provider := &LogicAppsStandardFrameworkServiceProvider{}
		svc := newServiceConfig("logicApp", "src/logicApp", nil)

		err := provider.Initialize(t.Context(), svc)
		require.NoError(t, err)
		assert.Equal(t, svc, provider.serviceConfig)
	})

	t.Run("succeeds when custom code project file exists", func(t *testing.T) {
		provider := &LogicAppsStandardFrameworkServiceProvider{}
		svc := newServiceConfig("logicApp", "src/logicApp", map[string]any{
			"customCodeProject": "Functions/Functions.csproj",
		})

		createFile(t, filepath.Join(projectDir, "src/logicApp/Functions/Functions.csproj"), "<Project />\n")

		withEnv(t, "AZD_EXEC_PROJECT_DIR", projectDir, func() {
			err := provider.Initialize(t.Context(), svc)
			require.NoError(t, err)
		})
	})

	t.Run("fails when custom code project is a directory", func(t *testing.T) {
		provider := &LogicAppsStandardFrameworkServiceProvider{}
		svc := newServiceConfig("logicApp", "src/logicApp", map[string]any{
			"customCodeProject": "Functions",
		})

		err := os.MkdirAll(filepath.Join(projectDir, "src/logicApp/Functions"), 0o750)
		require.NoError(t, err)

		withEnv(t, "AZD_EXEC_PROJECT_DIR", projectDir, func() {
			err := provider.Initialize(t.Context(), svc)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "must point to a file")
		})
	})

	t.Run("fails when custom code project file is missing", func(t *testing.T) {
		provider := &LogicAppsStandardFrameworkServiceProvider{}
		svc := newServiceConfig("logicApp", "src/logicApp", map[string]any{
			"customCodeProject": "Functions/Missing.csproj",
		})

		withEnv(t, "AZD_EXEC_PROJECT_DIR", projectDir, func() {
			err := provider.Initialize(t.Context(), svc)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "custom code project not found")
		})
	})

	t.Run("fails when custom code project escapes project directory", func(t *testing.T) {
		provider := &LogicAppsStandardFrameworkServiceProvider{}
		svc := newServiceConfig("logicApp", "src/logicApp", map[string]any{
			"customCodeProject": "../../../outside.csproj",
		})

		withEnv(t, "AZD_EXEC_PROJECT_DIR", projectDir, func() {
			err := provider.Initialize(t.Context(), svc)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "outside project directory")
		})
	})

	t.Run("fails when custom code project path is invalid", func(t *testing.T) {
		provider := &LogicAppsStandardFrameworkServiceProvider{}
		svc := newServiceConfig("logicApp", "src/logicApp", map[string]any{
			"customCodeProject": "Functions/Bad\x00Name.csproj",
		})

		withEnv(t, "AZD_EXEC_PROJECT_DIR", projectDir, func() {
			err := provider.Initialize(t.Context(), svc)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "custom code project")
		})
	})
}

func TestPackageUsesProjectAndOutputPaths(t *testing.T) {
	provider := &LogicAppsStandardFrameworkServiceProvider{}
	projectDir := t.TempDir()
	createFile(t, filepath.Join(projectDir, "azure.yaml"), "name: test-project\n")

	withEnv(t, "AZD_EXEC_PROJECT_DIR", projectDir, func() {
		result, err := provider.Package(t.Context(), newServiceConfig("logicApp", "src/logicApp", nil), nil, func(string) {})
		require.NoError(t, err)
		require.Len(t, result.Artifacts, 1)

		artifact := result.Artifacts[0]
		expectedPath := filepath.Join(projectDir, "src/logicApp")
		assert.Equal(t, expectedPath, artifact.Location)
		assert.Equal(t, azdext.ArtifactKind_ARTIFACT_KIND_DIRECTORY, artifact.Kind)
		assert.Equal(t, azdext.LocationKind_LOCATION_KIND_LOCAL, artifact.LocationKind)
	})

	withEnv(t, "AZD_EXEC_PROJECT_DIR", projectDir, func() {
		svc := newServiceConfig("logicApp", "src/logicApp", nil)
		svc.OutputPath = "Workflows"

		result, err := provider.Package(t.Context(), svc, nil, func(string) {})
		require.NoError(t, err)
		expectedPath := filepath.Join(projectDir, "src/logicApp", "Workflows")
		require.NotEmpty(t, result.Artifacts)
		assert.Equal(t, expectedPath, result.Artifacts[0].Location)
	})

	withEnv(t, "AZD_EXEC_PROJECT_DIR", projectDir, func() {
		svc := newServiceConfig("logicApp", "src/logicApp", nil)
		svc.OutputPath = "../../../outside"

		_, err := provider.Package(t.Context(), svc, nil, func(string) {})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside project directory")
	})
}

func TestRestoreAndBuildInvokeDotNetForCustomCodeProject(t *testing.T) {
	projectDir := t.TempDir()
	createFile(t, filepath.Join(projectDir, "azure.yaml"), "name: test-project\n")
	csprojPath := filepath.Join(projectDir, "src/logicApp/Functions/Functions.csproj")
	createFile(t, csprojPath, "<Project />\n")

	logFile := filepath.Join(t.TempDir(), "dotnet.log")
	fakeBinDir := t.TempDir()
	createFakeDotnetStub(t, fakeBinDir)

	svc := newServiceConfig("logicApp", "src/logicApp", map[string]any{
		"customCodeProject": "Functions/Functions.csproj",
	})
	provider := &LogicAppsStandardFrameworkServiceProvider{}

	withEnv(t, "AZD_EXEC_PROJECT_DIR", projectDir, func() {
		withEnv(t, "DOTNET_ARGS_LOG", logFile, func() {
			withEnv(t, "PATH", fakeBinDir+string(os.PathListSeparator)+os.Getenv("PATH"), func() {
				_, err := provider.Restore(t.Context(), svc, nil, func(string) {})
				require.NoError(t, err)
				_, err = provider.Build(t.Context(), svc, nil, func(string) {})
				require.NoError(t, err)
			})
		})
	})

	// #nosec G304 -- logFile points to a test-controlled file under t.TempDir().
	contents, err := os.ReadFile(logFile)
	require.NoError(t, err)
	normalized := strings.ReplaceAll(string(contents), "\r\n", "\n")
	logLines := strings.Split(strings.TrimSpace(normalized), "\n")
	require.Len(t, logLines, 2, "expected two dotnet invocations: %q", string(contents))

	expectedRestore := "restore " + csprojPath
	assert.Equal(t, expectedRestore, logLines[0])

	expectedBuild := "build " + csprojPath + " --configuration Release"
	assert.Equal(t, expectedBuild, logLines[1])
}

func TestRestoreAndBuildSkipDotNetWhenNoCustomCodeProject(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "dotnet.log")
	fakeBinDir := t.TempDir()
	createFakeDotnetStub(t, fakeBinDir)

	provider := &LogicAppsStandardFrameworkServiceProvider{}
	svc := newServiceConfig("logicApp", "src/logicApp", nil)

	withEnv(t, "DOTNET_ARGS_LOG", logFile, func() {
		withEnv(t, "PATH", fakeBinDir+string(os.PathListSeparator)+os.Getenv("PATH"), func() {
			_, err := provider.Restore(t.Context(), svc, nil, func(string) {})
			require.NoError(t, err)
			_, err = provider.Build(t.Context(), svc, nil, func(string) {})
			require.NoError(t, err)
		})
	})

	if _, statErr := os.Stat(logFile); statErr == nil {
		// #nosec G304 -- logFile points to a test-controlled file under t.TempDir().
		contents, readErr := os.ReadFile(logFile)
		require.NoError(t, readErr)
		assert.Empty(
			t,
			strings.TrimSpace(string(contents)),
			"dotnet should not be invoked when customCodeProject is not configured")
	} else {
		require.ErrorIs(t, statErr, os.ErrNotExist)
	}
}

func TestRestoreAndBuildReturnErrorWhenDotNetFails(t *testing.T) {
	projectDir := t.TempDir()
	createFile(t, filepath.Join(projectDir, "azure.yaml"), "name: test-project\n")
	csprojPath := filepath.Join(projectDir, "src/logicApp/Functions/Functions.csproj")
	createFile(t, csprojPath, "<Project />\n")

	fakeBinDir := t.TempDir()
	createFailingFakeDotnetStub(t, fakeBinDir)

	svc := newServiceConfig("logicApp", "src/logicApp", map[string]any{
		"customCodeProject": "Functions/Functions.csproj",
	})
	provider := &LogicAppsStandardFrameworkServiceProvider{}

	withEnv(t, "AZD_EXEC_PROJECT_DIR", projectDir, func() {
		withEnv(t, "PATH", fakeBinDir+string(os.PathListSeparator)+os.Getenv("PATH"), func() {
			_, err := provider.Restore(t.Context(), svc, nil, func(string) {})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "restoring custom code project")
			assert.Contains(t, err.Error(), csprojPath)

			_, err = provider.Build(t.Context(), svc, nil, func(string) {})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "building custom code project")
			assert.Contains(t, err.Error(), csprojPath)
		})
	})
}

func newServiceConfig(name, relativePath string, additionalProps map[string]any) *azdext.ServiceConfig {
	svc := &azdext.ServiceConfig{
		Name:         name,
		RelativePath: relativePath,
	}

	if additionalProps != nil {
		props, err := structpb.NewStruct(additionalProps)
		if err != nil {
			panic(err)
		}
		svc.AdditionalProperties = props
	}

	return svc
}

func withEnv(t *testing.T, key, value string, fn func()) {
	t.Helper()
	original, existed := os.LookupEnv(key)
	err := os.Setenv(key, value)
	require.NoError(t, err, "failed to set %s", key)
	t.Cleanup(func() {
		if !existed {
			_ = os.Unsetenv(key)
			return
		}
		_ = os.Setenv(key, original)
	})

	fn()
}

// createFakeDotnetStub places a fake dotnet executable in fakeBinDir.
// On Windows it creates dotnet.cmd (a batch file) so that cmd.exe can find
// and execute it via PATHEXT; on Unix it creates a dotnet shell script.
// Both stubs append their arguments to the file named by DOTNET_ARGS_LOG.
func createFakeDotnetStub(t *testing.T, fakeBinDir string) {
	t.Helper()
	stubName := "dotnet"
	stubContent := "#!/bin/sh\nprintf '%s\n' \"$*\" >> \"$DOTNET_ARGS_LOG\"\n"
	stubMode := os.FileMode(0o755)
	if runtime.GOOS == "windows" {
		stubName = "dotnet.cmd"
		stubContent = "@echo off\r\necho %* >> %DOTNET_ARGS_LOG%\r\n"
		stubMode = 0o644
	}
	stubPath := filepath.Join(fakeBinDir, stubName)
	err := os.WriteFile(stubPath, []byte(stubContent), stubMode)
	require.NoError(t, err, "failed writing fake dotnet stub %q", stubPath)
}

func createFailingFakeDotnetStub(t *testing.T, fakeBinDir string) {
	t.Helper()
	stubName := "dotnet"
	stubContent := "#!/bin/sh\nexit 1\n"
	stubMode := os.FileMode(0o755)
	if runtime.GOOS == "windows" {
		stubName = "dotnet.cmd"
		stubContent = "@echo off\r\nexit /b 1\r\n"
		stubMode = 0o644
	}
	stubPath := filepath.Join(fakeBinDir, stubName)
	err := os.WriteFile(stubPath, []byte(stubContent), stubMode)
	require.NoError(t, err, "failed writing failing fake dotnet stub %q", stubPath)
}

func createFile(t *testing.T, filePath, content string) {
	t.Helper()
	err := os.MkdirAll(filepath.Dir(filePath), 0o750)
	require.NoError(t, err, "failed creating directory %q", filepath.Dir(filePath))
	err = os.WriteFile(filePath, []byte(content), 0o600)
	require.NoError(t, err, "failed writing file %q", filePath)
}

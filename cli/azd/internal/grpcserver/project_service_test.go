// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

// Test_ProjectService_NoProject ensures that when no project exists,
// the service returns an error.
func Test_ProjectService_NoProject(t *testing.T) {
	// Setup a mock context.
	mockContext := mocks.NewMockContext(context.Background())

	// Mock GitHub CLI version check.
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: github.Version.String(),
	})

	// Lazy loaders return a "no project" error.
	lazyAzdContext := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return nil, azdcontext.ErrNoProject
	})
	lazyEnvManager := lazy.NewLazy(func() (environment.Manager, error) {
		return nil, azdcontext.ErrNoProject
	})
	lazyProjectConfig := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return nil, azdcontext.ErrNoProject
	})

	// Create mock GitHub CLI.
	ghCli, err := github.NewGitHubCli(*mockContext.Context, mockContext.Console, mockContext.CommandRunner)
	require.NoError(t, err)

	// Create the service with ImportManager.
	importManager := project.NewImportManager(&project.DotNetImporter{})
	service := NewProjectService(lazyAzdContext, lazyEnvManager, lazyProjectConfig, importManager, ghCli)
	_, err = service.Get(*mockContext.Context, &azdext.EmptyRequest{})
	require.Error(t, err)
}

// Test_ProjectService_Flow validates the complete project service flow:
// creating a project, setting environment variables and retrieving project details.
func Test_ProjectService_Flow(t *testing.T) {
	// Setup a mock context and temporary project directory.
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()

	// Mock GitHub CLI version check.
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: github.Version.String(),
	})

	// Initialize AzdContext with the temporary directory.
	azdContext := azdcontext.NewAzdContextWithDirectory(temp)

	// Define and save project configuration.
	projectConfig := project.ProjectConfig{
		Name: "test",
	}
	err := project.Save(*mockContext.Context, &projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

	// Configure and initialize environment manager.
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(mockContext.Container, azdContext, mockContext.Console, localDataStore, nil)
	require.NoError(t, err)
	require.NotNil(t, envManager)

	// Create lazy-loaded instances.
	lazyAzdContext := lazy.From(azdContext)
	lazyEnvManager := lazy.From(envManager)
	lazyProjectConfig := lazy.From(&projectConfig)

	// Create an environment and set an environment variable.
	testEnv1, err := envManager.Create(*mockContext.Context, environment.Spec{
		Name: "test1",
	})
	require.NoError(t, err)
	require.NotNil(t, testEnv1)
	testEnv1.DotenvSet("foo", "bar")
	err = envManager.Save(*mockContext.Context, testEnv1)
	require.NoError(t, err)

	// Create mock GitHub CLI.
	ghCli, err := github.NewGitHubCli(*mockContext.Context, mockContext.Console, mockContext.CommandRunner)
	require.NoError(t, err)

	// Create the service with ImportManager.
	importManager := project.NewImportManager(&project.DotNetImporter{})
	service := NewProjectService(lazyAzdContext, lazyEnvManager, lazyProjectConfig, importManager, ghCli)

	// Test: Retrieve project details.
	getResponse, err := service.Get(*mockContext.Context, &azdext.EmptyRequest{})
	require.NoError(t, err)
	require.NotNil(t, getResponse)
	require.Equal(t, projectConfig.Name, getResponse.Project.Name)
}

func Test_ProjectService_AddService(t *testing.T) {
	// Setup a mock context and temporary project directory.
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()

	// Mock GitHub CLI version check.
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, string(filepath.Separator)+"gh") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: github.Version.String(),
	})

	// Initialize AzdContext with the temporary directory.
	azdContext := azdcontext.NewAzdContextWithDirectory(temp)

	// Define and save project configuration.
	projectConfig := project.ProjectConfig{
		Name: "test",
	}
	err := project.Save(*mockContext.Context, &projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

	// Configure and initialize environment manager.
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(mockContext.Container, azdContext, mockContext.Console, localDataStore, nil)
	require.NoError(t, err)
	require.NotNil(t, envManager)

	// Create lazy-loaded instances.
	lazyAzdContext := lazy.From(azdContext)
	lazyEnvManager := lazy.From(envManager)
	lazyProjectConfig := lazy.From(&projectConfig)

	// Create mock GitHub CLI.
	ghCli, err := github.NewGitHubCli(*mockContext.Context, mockContext.Console, mockContext.CommandRunner)
	require.NoError(t, err)

	// Create the project service with ImportManager.
	importManager := project.NewImportManager(&project.DotNetImporter{})
	service := NewProjectService(lazyAzdContext, lazyEnvManager, lazyProjectConfig, importManager, ghCli)

	// Prepare a new service addition request.
	serviceRequest := &azdext.AddServiceRequest{
		Service: &azdext.ServiceConfig{
			Name:         "service1",
			RelativePath: filepath.Join("src", "service1"),
			Language:     "python",
			Host:         "containerapp",
		},
	}

	// Call AddService.
	emptyResponse, err := service.AddService(*mockContext.Context, serviceRequest)
	require.NoError(t, err)
	require.NotNil(t, emptyResponse)

	// Reload the project configuration and verify the service was added.
	updatedConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
	require.NoError(t, err)
	require.NotNil(t, updatedConfig.Services)
	serviceConfig, exists := updatedConfig.Services["service1"]
	require.True(t, exists)
	require.Equal(t, "service1", serviceConfig.Name)
	require.Equal(t, filepath.Join("src", "service1"), serviceConfig.RelativePath)
	require.Equal(t, project.ServiceLanguagePython, serviceConfig.Language)
	require.Equal(t, project.ContainerAppTarget, serviceConfig.Host)
}

func Test_ProjectService_ConfigSection(t *testing.T) {
	// Setup mock context and temporary project directory
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()
	azdContext := azdcontext.NewAzdContextWithDirectory(temp)

	// Create project config with additional properties
	projectConfig := &project.ProjectConfig{
		Name: "test",
		AdditionalProperties: map[string]any{
			"database": map[string]any{
				"host": "localhost",
				"port": 5432,
				"credentials": map[string]any{
					"username": "admin",
					"password": "secret",
				},
			},
			"feature": map[string]any{
				"enabled": true,
			},
		},
	}
	err := project.Save(*mockContext.Context, projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

	// Setup lazy dependencies
	lazyAzdContext := lazy.From(azdContext)
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(mockContext.Container, azdContext, mockContext.Console, localDataStore, nil)
	require.NoError(t, err)
	lazyEnvManager := lazy.From(envManager)
	lazyProjectConfig := lazy.From(projectConfig)

	importManager := project.NewImportManager(&project.DotNetImporter{})
	service := NewProjectService(lazyAzdContext, lazyEnvManager, lazyProjectConfig, importManager, nil)

	t.Run("GetConfigSection_Success", func(t *testing.T) {
		resp, err := service.GetConfigSection(*mockContext.Context, &azdext.GetProjectConfigSectionRequest{
			Path: "database",
		})
		require.NoError(t, err)
		require.True(t, resp.Found)
		require.NotNil(t, resp.Section)

		sectionMap := resp.Section.AsMap()
		require.Equal(t, "localhost", sectionMap["host"])
		require.Equal(t, float64(5432), sectionMap["port"]) // JSON numbers are float64
	})

	t.Run("GetConfigSection_NestedSection", func(t *testing.T) {
		resp, err := service.GetConfigSection(*mockContext.Context, &azdext.GetProjectConfigSectionRequest{
			Path: "database.credentials",
		})
		require.NoError(t, err)
		require.True(t, resp.Found)
		require.NotNil(t, resp.Section)

		sectionMap := resp.Section.AsMap()
		require.Equal(t, "admin", sectionMap["username"])
		require.Equal(t, "secret", sectionMap["password"])
	})

	t.Run("GetConfigSection_NotFound", func(t *testing.T) {
		resp, err := service.GetConfigSection(*mockContext.Context, &azdext.GetProjectConfigSectionRequest{
			Path: "nonexistent",
		})
		require.NoError(t, err)
		require.False(t, resp.Found)
		require.Nil(t, resp.Section)
	})
}

func Test_ProjectService_ConfigValue(t *testing.T) {
	// Setup mock context and temporary project directory
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()
	azdContext := azdcontext.NewAzdContextWithDirectory(temp)

	// Create project config with additional properties
	projectConfig := &project.ProjectConfig{
		Name: "test",
		AdditionalProperties: map[string]any{
			"database": map[string]any{
				"host":    "localhost",
				"port":    5432,
				"enabled": true,
			},
			"version": "1.0.0",
		},
	}
	err := project.Save(*mockContext.Context, projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

	// Setup lazy dependencies
	lazyAzdContext := lazy.From(azdContext)
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(mockContext.Container, azdContext, mockContext.Console, localDataStore, nil)
	require.NoError(t, err)
	lazyEnvManager := lazy.From(envManager)
	lazyProjectConfig := lazy.From(projectConfig)

	importManager := project.NewImportManager(&project.DotNetImporter{})
	service := NewProjectService(lazyAzdContext, lazyEnvManager, lazyProjectConfig, importManager, nil)

	t.Run("GetConfigValue_String", func(t *testing.T) {
		resp, err := service.GetConfigValue(*mockContext.Context, &azdext.GetProjectConfigValueRequest{
			Path: "version",
		})
		require.NoError(t, err)
		require.True(t, resp.Found)
		require.Equal(t, "1.0.0", resp.Value.AsInterface())
	})

	t.Run("GetConfigValue_NestedString", func(t *testing.T) {
		resp, err := service.GetConfigValue(*mockContext.Context, &azdext.GetProjectConfigValueRequest{
			Path: "database.host",
		})
		require.NoError(t, err)
		require.True(t, resp.Found)
		require.Equal(t, "localhost", resp.Value.AsInterface())
	})

	t.Run("GetConfigValue_Number", func(t *testing.T) {
		resp, err := service.GetConfigValue(*mockContext.Context, &azdext.GetProjectConfigValueRequest{
			Path: "database.port",
		})
		require.NoError(t, err)
		require.True(t, resp.Found)
		require.Equal(t, float64(5432), resp.Value.AsInterface())
	})

	t.Run("GetConfigValue_Boolean", func(t *testing.T) {
		resp, err := service.GetConfigValue(*mockContext.Context, &azdext.GetProjectConfigValueRequest{
			Path: "database.enabled",
		})
		require.NoError(t, err)
		require.True(t, resp.Found)
		require.Equal(t, true, resp.Value.AsInterface())
	})

	t.Run("GetConfigValue_NotFound", func(t *testing.T) {
		resp, err := service.GetConfigValue(*mockContext.Context, &azdext.GetProjectConfigValueRequest{
			Path: "nonexistent",
		})
		require.NoError(t, err)
		require.False(t, resp.Found)
		require.Nil(t, resp.Value)
	})
}

func Test_ProjectService_SetConfigSection(t *testing.T) {
	// Setup mock context and temporary project directory
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()
	azdContext := azdcontext.NewAzdContextWithDirectory(temp)

	// Create initial project config
	projectConfig := &project.ProjectConfig{
		Name: "test",
		AdditionalProperties: map[string]any{
			"existing": "value",
		},
	}
	err := project.Save(*mockContext.Context, projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

	// Setup lazy dependencies
	lazyAzdContext := lazy.From(azdContext)
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(mockContext.Container, azdContext, mockContext.Console, localDataStore, nil)
	require.NoError(t, err)
	lazyEnvManager := lazy.From(envManager)
	lazyProjectConfig := lazy.From(projectConfig)

	importManager := project.NewImportManager(&project.DotNetImporter{})
	service := NewProjectService(lazyAzdContext, lazyEnvManager, lazyProjectConfig, importManager, nil)

	t.Run("SetConfigSection_NewSection", func(t *testing.T) {
		// Create section data
		sectionData := map[string]any{
			"host": "newhost",
			"port": 3306,
			"ssl":  true,
		}
		sectionStruct, err := structpb.NewStruct(sectionData)
		require.NoError(t, err)

		// Set the section
		_, err = service.SetConfigSection(*mockContext.Context, &azdext.SetProjectConfigSectionRequest{
			Path:    "mysql",
			Section: sectionStruct,
		})
		require.NoError(t, err)

		// Verify section was set in the project config
		require.NotNil(t, projectConfig.AdditionalProperties["mysql"])
		mysqlSection := projectConfig.AdditionalProperties["mysql"].(map[string]any)
		require.Equal(t, "newhost", mysqlSection["host"])
		require.Equal(t, float64(3306), mysqlSection["port"])
		require.Equal(t, true, mysqlSection["ssl"])
	})

	t.Run("SetConfigSection_NestedSection", func(t *testing.T) {
		// Create nested section data
		sectionData := map[string]any{
			"username": "admin",
			"password": "secret123",
		}
		sectionStruct, err := structpb.NewStruct(sectionData)
		require.NoError(t, err)

		// Set the nested section
		_, err = service.SetConfigSection(*mockContext.Context, &azdext.SetProjectConfigSectionRequest{
			Path:    "mysql.credentials",
			Section: sectionStruct,
		})
		require.NoError(t, err)

		// Verify nested section was set
		mysqlSection := projectConfig.AdditionalProperties["mysql"].(map[string]any)
		credentials := mysqlSection["credentials"].(map[string]any)
		require.Equal(t, "admin", credentials["username"])
		require.Equal(t, "secret123", credentials["password"])
	})
}

func Test_ProjectService_SetConfigValue(t *testing.T) {
	// Setup mock context and temporary project directory
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()
	azdContext := azdcontext.NewAzdContextWithDirectory(temp)

	// Create initial project config
	projectConfig := &project.ProjectConfig{
		Name: "test",
		AdditionalProperties: map[string]any{
			"existing": "value",
		},
	}
	err := project.Save(*mockContext.Context, projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

	// Setup lazy dependencies
	lazyAzdContext := lazy.From(azdContext)
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(mockContext.Container, azdContext, mockContext.Console, localDataStore, nil)
	require.NoError(t, err)
	lazyEnvManager := lazy.From(envManager)
	lazyProjectConfig := lazy.From(projectConfig)

	importManager := project.NewImportManager(&project.DotNetImporter{})
	service := NewProjectService(lazyAzdContext, lazyEnvManager, lazyProjectConfig, importManager, nil)

	t.Run("SetConfigValue_String", func(t *testing.T) {
		value, err := structpb.NewValue("test-string")
		require.NoError(t, err)

		_, err = service.SetConfigValue(*mockContext.Context, &azdext.SetProjectConfigValueRequest{
			Path:  "app.name",
			Value: value,
		})
		require.NoError(t, err)

		// Verify value was set
		appSection := projectConfig.AdditionalProperties["app"].(map[string]any)
		require.Equal(t, "test-string", appSection["name"])
	})

	t.Run("SetConfigValue_Number", func(t *testing.T) {
		value, err := structpb.NewValue(8080)
		require.NoError(t, err)

		_, err = service.SetConfigValue(*mockContext.Context, &azdext.SetProjectConfigValueRequest{
			Path:  "app.port",
			Value: value,
		})
		require.NoError(t, err)

		// Verify value was set
		appSection := projectConfig.AdditionalProperties["app"].(map[string]any)
		require.Equal(t, float64(8080), appSection["port"])
	})

	t.Run("SetConfigValue_Boolean", func(t *testing.T) {
		value, err := structpb.NewValue(true)
		require.NoError(t, err)

		_, err = service.SetConfigValue(*mockContext.Context, &azdext.SetProjectConfigValueRequest{
			Path:  "app.debug",
			Value: value,
		})
		require.NoError(t, err)

		// Verify value was set
		appSection := projectConfig.AdditionalProperties["app"].(map[string]any)
		require.Equal(t, true, appSection["debug"])
	})
}

func Test_ProjectService_UnsetConfig(t *testing.T) {
	// Setup mock context and temporary project directory
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()
	azdContext := azdcontext.NewAzdContextWithDirectory(temp)

	// Create project config with additional properties to unset
	projectConfig := &project.ProjectConfig{
		Name: "test",
		AdditionalProperties: map[string]any{
			"database": map[string]any{
				"host": "localhost",
				"port": 5432,
				"credentials": map[string]any{
					"username": "admin",
					"password": "secret",
				},
			},
			"cache": map[string]any{
				"enabled": true,
				"ttl":     300,
			},
		},
	}
	err := project.Save(*mockContext.Context, projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

	// Setup lazy dependencies
	lazyAzdContext := lazy.From(azdContext)
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(mockContext.Container, azdContext, mockContext.Console, localDataStore, nil)
	require.NoError(t, err)
	lazyEnvManager := lazy.From(envManager)
	lazyProjectConfig := lazy.From(projectConfig)

	importManager := project.NewImportManager(&project.DotNetImporter{})
	service := NewProjectService(lazyAzdContext, lazyEnvManager, lazyProjectConfig, importManager, nil)

	t.Run("UnsetConfig_NestedValue", func(t *testing.T) {
		_, err := service.UnsetConfig(*mockContext.Context, &azdext.UnsetProjectConfigRequest{
			Path: "database.credentials.password",
		})
		require.NoError(t, err)

		// Verify nested value was removed
		databaseSection := projectConfig.AdditionalProperties["database"].(map[string]any)
		credentials := databaseSection["credentials"].(map[string]any)
		_, exists := credentials["password"]
		require.False(t, exists)
		// But username should still exist
		require.Equal(t, "admin", credentials["username"])
	})

	t.Run("UnsetConfig_EntireSection", func(t *testing.T) {
		_, err := service.UnsetConfig(*mockContext.Context, &azdext.UnsetProjectConfigRequest{
			Path: "cache",
		})
		require.NoError(t, err)

		// Verify entire section was removed
		_, exists := projectConfig.AdditionalProperties["cache"]
		require.False(t, exists)
		// But database section should still exist
		_, exists = projectConfig.AdditionalProperties["database"]
		require.True(t, exists)
	})

	t.Run("UnsetConfig_NonexistentPath", func(t *testing.T) {
		_, err := service.UnsetConfig(*mockContext.Context, &azdext.UnsetProjectConfigRequest{
			Path: "nonexistent.path",
		})
		// Should not error even if path doesn't exist
		require.NoError(t, err)
	})
}

func Test_ProjectService_ConfigNilAdditionalProperties(t *testing.T) {
	// Test behavior when AdditionalProperties is nil
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()
	azdContext := azdcontext.NewAzdContextWithDirectory(temp)

	// Create project config WITHOUT additional properties
	projectConfig := &project.ProjectConfig{
		Name: "test",
		// AdditionalProperties is nil
	}
	err := project.Save(*mockContext.Context, projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

	// Setup lazy dependencies
	lazyAzdContext := lazy.From(azdContext)
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(mockContext.Container, azdContext, mockContext.Console, localDataStore, nil)
	require.NoError(t, err)
	lazyEnvManager := lazy.From(envManager)
	lazyProjectConfig := lazy.From(projectConfig)

	importManager := project.NewImportManager(&project.DotNetImporter{})
	service := NewProjectService(lazyAzdContext, lazyEnvManager, lazyProjectConfig, importManager, nil)

	t.Run("GetConfigValue_NilAdditionalProperties", func(t *testing.T) {
		resp, err := service.GetConfigValue(*mockContext.Context, &azdext.GetProjectConfigValueRequest{
			Path: "any.path",
		})
		require.NoError(t, err)
		require.False(t, resp.Found)
	})

	t.Run("SetConfigValue_NilAdditionalProperties", func(t *testing.T) {
		value, err := structpb.NewValue("test-value")
		require.NoError(t, err)

		_, err = service.SetConfigValue(*mockContext.Context, &azdext.SetProjectConfigValueRequest{
			Path:  "new.value",
			Value: value,
		})
		require.NoError(t, err)

		// Verify AdditionalProperties was initialized and value was set
		require.NotNil(t, projectConfig.AdditionalProperties)
		newSection := projectConfig.AdditionalProperties["new"].(map[string]any)
		require.Equal(t, "test-value", newSection["value"])
	})
}

// Test_ProjectService_ServiceConfiguration validates service-level configuration operations.
func Test_ProjectService_ServiceConfiguration(t *testing.T) {
	// Setup a mock context and temporary project directory.
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()

	// Initialize project configuration with a service.
	projectConfig := &project.ProjectConfig{
		Name: "test-project",
		Path: temp,
		Services: map[string]*project.ServiceConfig{
			"api": {
				Name:       "api",
				Host:       project.ContainerAppTarget,
				Language:   "javascript",
				OutputPath: "./dist",
				AdditionalProperties: map[string]any{
					"custom": map[string]any{
						"setting": "value",
						"nested": map[string]any{
							"key": "nested-value",
						},
					},
					"database": map[string]any{
						"host": "localhost",
						"port": float64(5432), // JSON numbers become float64
					},
				},
			},
			"web": {
				Name:     "web",
				Host:     project.StaticWebAppTarget,
				Language: "typescript",
			},
		},
	}

	// Mock AzdContext with project path.
	azdContext := &azdcontext.AzdContext{}
	azdContext.SetProjectDirectory(temp)

	// Configure and initialize environment manager.
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(mockContext.Container, azdContext, mockContext.Console, localDataStore, nil)
	require.NoError(t, err)
	require.NotNil(t, envManager)

	// Create lazy loaders.
	lazyAzdContext := lazy.From(azdContext)
	lazyEnvManager := lazy.From(envManager)
	lazyProjectConfig := lazy.From(projectConfig)

	// Create the service.
	importManager := project.NewImportManager(&project.DotNetImporter{})
	service := NewProjectService(lazyAzdContext, lazyEnvManager, lazyProjectConfig, importManager, nil)

	t.Run("GetServiceConfigSection_Found", func(t *testing.T) {
		resp, err := service.GetServiceConfigSection(*mockContext.Context, &azdext.GetServiceConfigSectionRequest{
			ServiceName: "api",
			Path:        "custom",
		})
		require.NoError(t, err)
		require.True(t, resp.Found)
		require.NotNil(t, resp.Section)

		// Verify the section content
		sectionMap := resp.Section.AsMap()
		require.Equal(t, "value", sectionMap["setting"])

		nested := sectionMap["nested"].(map[string]any)
		require.Equal(t, "nested-value", nested["key"])
	})

	t.Run("GetServiceConfigSection_NotFound", func(t *testing.T) {
		resp, err := service.GetServiceConfigSection(*mockContext.Context, &azdext.GetServiceConfigSectionRequest{
			ServiceName: "api",
			Path:        "nonexistent",
		})
		require.NoError(t, err)
		require.False(t, resp.Found)
		require.Nil(t, resp.Section)
	})

	t.Run("GetServiceConfigSection_ServiceNotFound", func(t *testing.T) {
		_, err := service.GetServiceConfigSection(*mockContext.Context, &azdext.GetServiceConfigSectionRequest{
			ServiceName: "nonexistent",
			Path:        "custom",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "service 'nonexistent' not found")
	})

	t.Run("GetServiceConfigValue_Found", func(t *testing.T) {
		resp, err := service.GetServiceConfigValue(*mockContext.Context, &azdext.GetServiceConfigValueRequest{
			ServiceName: "api",
			Path:        "custom.setting",
		})
		require.NoError(t, err)
		require.True(t, resp.Found)
		require.NotNil(t, resp.Value)
		require.Equal(t, "value", resp.Value.AsInterface())
	})

	t.Run("GetServiceConfigValue_NestedValue", func(t *testing.T) {
		resp, err := service.GetServiceConfigValue(*mockContext.Context, &azdext.GetServiceConfigValueRequest{
			ServiceName: "api",
			Path:        "custom.nested.key",
		})
		require.NoError(t, err)
		require.True(t, resp.Found)
		require.NotNil(t, resp.Value)
		require.Equal(t, "nested-value", resp.Value.AsInterface())
	})

	t.Run("GetServiceConfigValue_NumericValue", func(t *testing.T) {
		resp, err := service.GetServiceConfigValue(*mockContext.Context, &azdext.GetServiceConfigValueRequest{
			ServiceName: "api",
			Path:        "database.port",
		})
		require.NoError(t, err)
		require.True(t, resp.Found)
		require.NotNil(t, resp.Value)
		require.Equal(t, float64(5432), resp.Value.AsInterface())
	})

	t.Run("GetServiceConfigValue_NotFound", func(t *testing.T) {
		resp, err := service.GetServiceConfigValue(*mockContext.Context, &azdext.GetServiceConfigValueRequest{
			ServiceName: "api",
			Path:        "nonexistent.path",
		})
		require.NoError(t, err)
		require.False(t, resp.Found)
		require.Nil(t, resp.Value)
	})

	t.Run("GetServiceConfigValue_ServiceNotFound", func(t *testing.T) {
		_, err := service.GetServiceConfigValue(*mockContext.Context, &azdext.GetServiceConfigValueRequest{
			ServiceName: "nonexistent",
			Path:        "custom.setting",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "service 'nonexistent' not found")
	})

	t.Run("SetServiceConfigSection", func(t *testing.T) {
		sectionData := map[string]any{
			"newSetting": "new-value",
			"anotherSetting": map[string]any{
				"innerKey": "inner-value",
			},
		}
		sectionStruct, err := structpb.NewStruct(sectionData)
		require.NoError(t, err)

		_, err = service.SetServiceConfigSection(*mockContext.Context, &azdext.SetServiceConfigSectionRequest{
			ServiceName: "api",
			Path:        "newSection",
			Section:     sectionStruct,
		})
		require.NoError(t, err)

		// Verify the section was set
		apiService := projectConfig.Services["api"]
		require.NotNil(t, apiService.AdditionalProperties)
		newSection := apiService.AdditionalProperties["newSection"].(map[string]any)
		require.Equal(t, "new-value", newSection["newSetting"])

		anotherSetting := newSection["anotherSetting"].(map[string]any)
		require.Equal(t, "inner-value", anotherSetting["innerKey"])
	})

	t.Run("SetServiceConfigSection_ServiceNotFound", func(t *testing.T) {
		sectionStruct, err := structpb.NewStruct(map[string]any{"key": "value"})
		require.NoError(t, err)

		_, err = service.SetServiceConfigSection(*mockContext.Context, &azdext.SetServiceConfigSectionRequest{
			ServiceName: "nonexistent",
			Path:        "section",
			Section:     sectionStruct,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "service 'nonexistent' not found")
	})

	t.Run("SetServiceConfigValue", func(t *testing.T) {
		value, err := structpb.NewValue("updated-value")
		require.NoError(t, err)

		_, err = service.SetServiceConfigValue(*mockContext.Context, &azdext.SetServiceConfigValueRequest{
			ServiceName: "api",
			Path:        "custom.setting",
			Value:       value,
		})
		require.NoError(t, err)

		// Verify the value was updated
		apiService := projectConfig.Services["api"]
		customSection := apiService.AdditionalProperties["custom"].(map[string]any)
		require.Equal(t, "updated-value", customSection["setting"])
	})

	t.Run("SetServiceConfigValue_NewPath", func(t *testing.T) {
		value, err := structpb.NewValue(float64(8080))
		require.NoError(t, err)

		_, err = service.SetServiceConfigValue(*mockContext.Context, &azdext.SetServiceConfigValueRequest{
			ServiceName: "api",
			Path:        "server.port",
			Value:       value,
		})
		require.NoError(t, err)

		// Verify the new path was created
		apiService := projectConfig.Services["api"]
		serverSection := apiService.AdditionalProperties["server"].(map[string]any)
		require.Equal(t, float64(8080), serverSection["port"])
	})

	t.Run("SetServiceConfigValue_ServiceNotFound", func(t *testing.T) {
		value, err := structpb.NewValue("value")
		require.NoError(t, err)

		_, err = service.SetServiceConfigValue(*mockContext.Context, &azdext.SetServiceConfigValueRequest{
			ServiceName: "nonexistent",
			Path:        "path",
			Value:       value,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "service 'nonexistent' not found")
	})

	t.Run("UnsetServiceConfig", func(t *testing.T) {
		_, err := service.UnsetServiceConfig(*mockContext.Context, &azdext.UnsetServiceConfigRequest{
			ServiceName: "api",
			Path:        "custom.setting",
		})
		require.NoError(t, err)

		// Verify the value was removed
		apiService := projectConfig.Services["api"]
		customSection := apiService.AdditionalProperties["custom"].(map[string]any)
		_, exists := customSection["setting"]
		require.False(t, exists)
	})

	t.Run("UnsetServiceConfig_EntireSection", func(t *testing.T) {
		_, err := service.UnsetServiceConfig(*mockContext.Context, &azdext.UnsetServiceConfigRequest{
			ServiceName: "api",
			Path:        "database",
		})
		require.NoError(t, err)

		// Verify the entire section was removed
		apiService := projectConfig.Services["api"]
		_, exists := apiService.AdditionalProperties["database"]
		require.False(t, exists)
	})

	t.Run("UnsetServiceConfig_ServiceNotFound", func(t *testing.T) {
		_, err := service.UnsetServiceConfig(*mockContext.Context, &azdext.UnsetServiceConfigRequest{
			ServiceName: "nonexistent",
			Path:        "path",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "service 'nonexistent' not found")
	})

	t.Run("UnsetServiceConfig_NonexistentPath", func(t *testing.T) {
		_, err := service.UnsetServiceConfig(*mockContext.Context, &azdext.UnsetServiceConfigRequest{
			ServiceName: "api",
			Path:        "nonexistent.path",
		})
		require.NoError(t, err) // Should not error even if path doesn't exist
	})
}

// Test_ProjectService_ServiceConfiguration_NilAdditionalProperties validates service configuration
// operations when AdditionalProperties is nil.
func Test_ProjectService_ServiceConfiguration_NilAdditionalProperties(t *testing.T) {
	// Setup a mock context and temporary project directory.
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()

	// Initialize project configuration with a service that has nil AdditionalProperties.
	projectConfig := &project.ProjectConfig{
		Name: "test-project",
		Path: temp,
		Services: map[string]*project.ServiceConfig{
			"api": {
				Name:     "api",
				Host:     project.ContainerAppTarget,
				Language: "javascript",
				// AdditionalProperties is nil
			},
		},
	}

	// Mock AzdContext with project path.
	azdContext := &azdcontext.AzdContext{}
	azdContext.SetProjectDirectory(temp)

	// Configure and initialize environment manager.
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(mockContext.Container, azdContext, mockContext.Console, localDataStore, nil)
	require.NoError(t, err)
	require.NotNil(t, envManager)

	// Create lazy loaders.
	lazyAzdContext := lazy.From(azdContext)
	lazyEnvManager := lazy.From(envManager)
	lazyProjectConfig := lazy.From(projectConfig)

	// Create the service.
	importManager := project.NewImportManager(&project.DotNetImporter{})
	service := NewProjectService(lazyAzdContext, lazyEnvManager, lazyProjectConfig, importManager, nil)

	t.Run("GetServiceConfigSection_NilAdditionalProperties", func(t *testing.T) {
		resp, err := service.GetServiceConfigSection(*mockContext.Context, &azdext.GetServiceConfigSectionRequest{
			ServiceName: "api",
			Path:        "any.path",
		})
		require.NoError(t, err)
		require.False(t, resp.Found)
	})

	t.Run("GetServiceConfigValue_NilAdditionalProperties", func(t *testing.T) {
		resp, err := service.GetServiceConfigValue(*mockContext.Context, &azdext.GetServiceConfigValueRequest{
			ServiceName: "api",
			Path:        "any.path",
		})
		require.NoError(t, err)
		require.False(t, resp.Found)
	})

	t.Run("SetServiceConfigValue_NilAdditionalProperties", func(t *testing.T) {
		value, err := structpb.NewValue("test-value")
		require.NoError(t, err)

		_, err = service.SetServiceConfigValue(*mockContext.Context, &azdext.SetServiceConfigValueRequest{
			ServiceName: "api",
			Path:        "new.value",
			Value:       value,
		})
		require.NoError(t, err)

		// Verify AdditionalProperties was initialized and value was set
		apiService := projectConfig.Services["api"]
		require.NotNil(t, apiService.AdditionalProperties)
		newSection := apiService.AdditionalProperties["new"].(map[string]any)
		require.Equal(t, "test-value", newSection["value"])
	})
}

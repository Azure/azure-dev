// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
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

		// Reload config from disk to verify changes were persisted
		cfg, err := project.LoadConfig(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)

		mysqlSection, found := cfg.GetMap("mysql")
		require.True(t, found, "mysql section should exist")
		require.Equal(t, "newhost", mysqlSection["host"])
		require.Equal(t, 3306, mysqlSection["port"])
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

		// Reload config from disk to verify changes were persisted
		cfg, err := project.LoadConfig(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)

		credentials, found := cfg.GetMap("mysql.credentials")
		require.True(t, found, "mysql.credentials section should exist")
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

		// Reload config from disk to verify value was set
		cfg, err := project.LoadConfig(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)

		name, found := cfg.Get("app.name")
		require.True(t, found, "app.name should exist")
		require.Equal(t, "test-string", name)
	})

	t.Run("SetConfigValue_Number", func(t *testing.T) {
		value, err := structpb.NewValue(8080)
		require.NoError(t, err)

		_, err = service.SetConfigValue(*mockContext.Context, &azdext.SetProjectConfigValueRequest{
			Path:  "app.port",
			Value: value,
		})
		require.NoError(t, err)

		// Reload config from disk to verify value was set
		cfg, err := project.LoadConfig(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)

		port, found := cfg.Get("app.port")
		require.True(t, found, "app.port should exist")
		require.Equal(t, 8080, port)
	})

	t.Run("SetConfigValue_Boolean", func(t *testing.T) {
		value, err := structpb.NewValue(true)
		require.NoError(t, err)

		_, err = service.SetConfigValue(*mockContext.Context, &azdext.SetProjectConfigValueRequest{
			Path:  "app.debug",
			Value: value,
		})
		require.NoError(t, err)

		// Reload config from disk to verify value was set
		cfg, err := project.LoadConfig(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)

		debug, found := cfg.Get("app.debug")
		require.True(t, found, "app.debug should exist")
		require.Equal(t, true, debug)
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

		// Reload config from disk to verify nested value was removed
		cfg, err := project.LoadConfig(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)

		_, exists := cfg.Get("database.credentials.password")
		require.False(t, exists, "password should be removed")
		// But username should still exist
		username, exists := cfg.Get("database.credentials.username")
		require.True(t, exists, "username should still exist")
		require.Equal(t, "admin", username)
	})

	t.Run("UnsetConfig_EntireSection", func(t *testing.T) {
		_, err := service.UnsetConfig(*mockContext.Context, &azdext.UnsetProjectConfigRequest{
			Path: "cache",
		})
		require.NoError(t, err)

		// Reload config from disk to verify entire section was removed
		cfg, err := project.LoadConfig(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)

		_, exists := cfg.GetMap("cache")
		require.False(t, exists, "cache section should be removed")
		// But database section should still exist
		_, exists = cfg.GetMap("database")
		require.True(t, exists, "database section should still exist")
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

		// Reload config from disk to verify value was set
		cfg, err := project.LoadConfig(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)

		val, found := cfg.Get("new.value")
		require.True(t, found, "new.value should exist")
		require.Equal(t, "test-value", val)
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

	// Save the initial project config to disk
	err := project.Save(*mockContext.Context, projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

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

		// Verify the section was set by loading from disk
		cfg, err := project.LoadConfig(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)

		newSection, found := cfg.GetMap("services.api.newSection")
		require.True(t, found, "services.api.newSection should exist")
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

		// Verify the value was updated by loading from disk
		cfg, err := project.LoadConfig(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)

		updatedValue, found := cfg.Get("services.api.custom.setting")
		require.True(t, found, "services.api.custom.setting should exist")
		require.Equal(t, "updated-value", updatedValue)
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

		// Verify the new path was created by loading from disk
		cfg, err := project.LoadConfig(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)

		portValue, found := cfg.Get("services.api.server.port")
		require.True(t, found, "services.api.server.port should exist")
		require.Equal(t, 8080, portValue)
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

		// Verify the value was removed by loading from disk
		cfg, err := project.LoadConfig(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)

		_, found := cfg.Get("services.api.custom.setting")
		require.False(t, found, "services.api.custom.setting should not exist after unset")
	})

	t.Run("UnsetServiceConfig_EntireSection", func(t *testing.T) {
		_, err := service.UnsetServiceConfig(*mockContext.Context, &azdext.UnsetServiceConfigRequest{
			ServiceName: "api",
			Path:        "database",
		})
		require.NoError(t, err)

		// Verify the entire section was removed by loading from disk
		cfg, err := project.LoadConfig(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)

		_, found := cfg.Get("services.api.database")
		require.False(t, found, "services.api.database should not exist after unset")
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

	// Save the initial project config to disk
	err := project.Save(*mockContext.Context, projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

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

		// Verify AdditionalProperties was initialized and value was set by loading from disk
		cfg, err := project.LoadConfig(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)

		val, found := cfg.Get("services.api.new.value")
		require.True(t, found, "services.api.new.value should exist")
		require.Equal(t, "test-value", val)
	})
}

// Test_ProjectService_ChangeServiceHost validates that core service configuration fields
// (like "host") can be retrieved and modified using the config methods after migrating
// to the unified LoadConfig/SaveConfig approach.
func Test_ProjectService_ChangeServiceHost(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()
	azdContext := azdcontext.NewAzdContextWithDirectory(temp)

	// Create project with a service that has host=containerapp
	projectConfig := &project.ProjectConfig{
		Name: "test",
		Services: map[string]*project.ServiceConfig{
			"web": {
				Name:         "web",
				Host:         project.ContainerAppTarget,
				Language:     project.ServiceLanguageTypeScript,
				RelativePath: "./src/web",
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

	// Test 1: Get the current host value
	getResp, err := service.GetServiceConfigValue(*mockContext.Context, &azdext.GetServiceConfigValueRequest{
		ServiceName: "web",
		Path:        "host",
	})
	require.NoError(t, err)
	require.True(t, getResp.Found, "host field should be found")
	require.Equal(t, string(project.ContainerAppTarget), getResp.Value.GetStringValue(),
		"host should be 'containerapp'")

	// Test 2: Change the host to appservice
	value, err := structpb.NewValue(string(project.AppServiceTarget))
	require.NoError(t, err)

	_, err = service.SetServiceConfigValue(*mockContext.Context, &azdext.SetServiceConfigValueRequest{
		ServiceName: "web",
		Path:        "host",
		Value:       value,
	})
	require.NoError(t, err, "setting core field 'host' should succeed")

	// Test 3: Verify the host was changed
	getResp2, err := service.GetServiceConfigValue(*mockContext.Context, &azdext.GetServiceConfigValueRequest{
		ServiceName: "web",
		Path:        "host",
	})
	require.NoError(t, err)
	require.True(t, getResp2.Found, "host field should still be found")
	require.Equal(t, string(project.AppServiceTarget), getResp2.Value.GetStringValue(),
		"host should now be 'appservice'")

	// Test 4: Verify the change was persisted to disk
	reloadedConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
	require.NoError(t, err)
	require.Equal(t, project.AppServiceTarget, reloadedConfig.Services["web"].Host,
		"persisted host should be 'appservice'")
}

// Test_ProjectService_TypeValidation_InvalidChangesNotPersisted tests that invalid type changes
// fail validation and are not persisted to disk.
func Test_ProjectService_TypeValidation_InvalidChangesNotPersisted(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()

	azdContext := azdcontext.NewAzdContextWithDirectory(temp)

	// Create initial project with a service
	projectConfig := &project.ProjectConfig{
		Name: "test-project",
		Services: map[string]*project.ServiceConfig{
			"web": {
				Name:         "web",
				RelativePath: "./src/web",
				Host:         project.ContainerAppTarget,
				Language:     project.ServiceLanguageDotNet,
			},
		},
	}

	err := project.Save(*mockContext.Context, projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

	loadedConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
	require.NoError(t, err)

	// Setup lazy dependencies
	lazyAzdContext := lazy.From(azdContext)
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(
		mockContext.Container,
		azdContext,
		mockContext.Console,
		localDataStore,
		nil,
	)
	require.NoError(t, err)
	lazyEnvManager := lazy.From(envManager)
	lazyProjectConfig := lazy.From(loadedConfig)

	importManager := project.NewImportManager(&project.DotNetImporter{})
	service := NewProjectService(lazyAzdContext, lazyEnvManager, lazyProjectConfig, importManager, nil)

	t.Run("Project_SetInfraToInt_ShouldFailAndNotPersist", func(t *testing.T) {
		// Try to set "infra" (which should be an object) to an integer
		intValue, err := structpb.NewValue(123)
		require.NoError(t, err)

		_, err = service.SetConfigValue(*mockContext.Context, &azdext.SetProjectConfigValueRequest{
			Path:  "infra",
			Value: intValue,
		})

		// This should fail because "infra" expects a provisioning.Options struct, not an int
		require.Error(t, err, "setting infra to int should fail validation")

		// Verify the change was NOT persisted to disk
		reloadedConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)
		require.NotNil(t, reloadedConfig.Infra, "infra should still be valid object")
		require.Empty(t, reloadedConfig.Infra.Provider, "infra.provider should be empty (default)")
	})

	t.Run("Project_SetInfraProviderToObject_ShouldFailAndNotPersist", func(t *testing.T) {
		// Try to set "infra.provider" (which should be a string) to an object
		objectValue, err := structpb.NewStruct(map[string]interface{}{
			"nested": "value",
		})
		require.NoError(t, err)

		_, err = service.SetConfigValue(*mockContext.Context, &azdext.SetProjectConfigValueRequest{
			Path:  "infra.provider",
			Value: structpb.NewStructValue(objectValue),
		})

		// This should fail because "infra.provider" expects a string, not an object
		require.Error(t, err, "setting infra.provider to object should fail validation")

		// Verify the change was NOT persisted to disk
		reloadedConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)
		require.Empty(t, reloadedConfig.Infra.Provider, "infra.provider should still be empty")
	})

	t.Run("Project_SetInfraProviderToInt_FailsDuringSet", func(t *testing.T) {
		// Try to set "infra.provider" to an int instead of a string
		invalidProvider, err := structpb.NewValue(999)
		require.NoError(t, err)

		_, err = service.SetConfigValue(*mockContext.Context, &azdext.SetProjectConfigValueRequest{
			Path:  "infra.provider",
			Value: invalidProvider,
		})

		// SetConfigValue calls reloadAndCacheProjectConfig which calls project.Load
		// project.Load fails because "999" is not a valid provider
		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported IaC provider '999'")

		// Verify the change was NOT persisted to disk (should still be valid)
		reloadedConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)
		require.Empty(t, reloadedConfig.Infra.Provider)
	})

	t.Run("Service_SetHostToInt_CoercesToString", func(t *testing.T) {
		// Save the current state
		originalConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)
		originalHost := originalConfig.Services["web"].Host

		// Try to set "host" to an integer instead of a string
		invalidValue, err := structpb.NewValue(789)
		require.NoError(t, err)

		_, err = service.SetServiceConfigValue(*mockContext.Context, &azdext.SetServiceConfigValueRequest{
			ServiceName: "web",
			Path:        "host",
			Value:       invalidValue,
		})

		// This succeeds at the config level (YAML allows numbers)
		require.NoError(t, err)

		// YAML coerces 789 to string "789", which is then treated as a custom host value
		// (project.Load doesn't fail on unknown host types, it treats them as custom)
		reloadedConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)
		require.Equal(t, project.ServiceTargetKind("789"), reloadedConfig.Services["web"].Host)

		// Restore the original valid configuration
		err = project.Save(*mockContext.Context, originalConfig, azdContext.ProjectPath())
		require.NoError(t, err)

		// Verify restoration succeeded
		restoredConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)
		require.Equal(t, originalHost, restoredConfig.Services["web"].Host)
	})

	t.Run("Service_SetLanguageToArray_ShouldFailAndNotPersist", func(t *testing.T) {
		// Get current language value
		originalConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)
		originalLanguage := originalConfig.Services["web"].Language

		// Try to set "language" to an array
		arrayValue, err := structpb.NewList([]interface{}{"go", "python"})
		require.NoError(t, err)

		_, err = service.SetServiceConfigValue(*mockContext.Context, &azdext.SetServiceConfigValueRequest{
			ServiceName: "web",
			Path:        "language",
			Value:       structpb.NewListValue(arrayValue),
		})

		// This should fail because "language" expects a string, not an array
		require.Error(t, err, "setting language to array should fail validation")

		// Verify the change was NOT persisted to disk
		reloadedConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)
		require.Equal(t, originalLanguage, reloadedConfig.Services["web"].Language,
			"language should still have original value")
	})

	t.Run("Service_SetDockerToInvalidStructure_ShouldSucceedButFailOnReload", func(t *testing.T) {
		// Save the current state
		originalConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)

		// Try to set "docker.path" to an int instead of a string
		invalidPath, err := structpb.NewValue(123)
		require.NoError(t, err)

		_, err = service.SetServiceConfigValue(*mockContext.Context, &azdext.SetServiceConfigValueRequest{
			ServiceName: "web",
			Path:        "docker.path",
			Value:       invalidPath,
		})

		// This succeeds at the config level (YAML allows numbers)
		require.NoError(t, err, "setting docker.path to int succeeds at config level")

		// When we reload, YAML will coerce 123 to string "123", which is technically valid
		// but semantically wrong (not a valid file path)
		reloadedConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err, "parsing succeeds because YAML coerces int to string")
		require.Equal(t, "123", reloadedConfig.Services["web"].Docker.Path, "path is coerced to string '123'")

		// Restore the original valid configuration
		err = project.Save(*mockContext.Context, originalConfig, azdContext.ProjectPath())
		require.NoError(t, err)

		// Verify restoration succeeded
		restoredConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)
		require.Empty(t, restoredConfig.Services["web"].Docker.Path)
	})
}

// Test_ProjectService_TypeValidation_CoercedValues tests YAML type coercion behavior
func Test_ProjectService_TypeValidation_CoercedValues(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()

	azdContext := azdcontext.NewAzdContextWithDirectory(temp)

	// Create initial project
	projectConfig := &project.ProjectConfig{
		Name: "test-project",
	}

	err := project.Save(*mockContext.Context, projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

	loadedConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
	require.NoError(t, err)

	// Setup lazy dependencies
	lazyAzdContext := lazy.From(azdContext)
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(
		mockContext.Container,
		azdContext,
		mockContext.Console,
		localDataStore,
		nil,
	)
	require.NoError(t, err)
	lazyEnvManager := lazy.From(envManager)
	lazyProjectConfig := lazy.From(loadedConfig)

	importManager := project.NewImportManager(&project.DotNetImporter{})
	service := NewProjectService(lazyAzdContext, lazyEnvManager, lazyProjectConfig, importManager, nil)

	t.Run("SetNameToInt_GetsCoercedToString", func(t *testing.T) {
		// Try to set "name" (which should be a string) to an integer
		intValue, err := structpb.NewValue(456)
		require.NoError(t, err)

		_, err = service.SetConfigValue(*mockContext.Context, &azdext.SetProjectConfigValueRequest{
			Path:  "name",
			Value: intValue,
		})

		// YAML will coerce the int to a string, so this succeeds
		require.NoError(t, err, "YAML coerces int to string, so this succeeds")

		// When loaded as ProjectConfig, it gets coerced to string "456"
		reloadedConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)
		require.Equal(t, "456", reloadedConfig.Name, "YAML unmarshals int as string '456'")
	})

	t.Run("SetNameToBool_GetsCoercedToString", func(t *testing.T) {
		// Try to set "name" to a boolean
		boolValue, err := structpb.NewValue(true)
		require.NoError(t, err)

		_, err = service.SetConfigValue(*mockContext.Context, &azdext.SetProjectConfigValueRequest{
			Path:  "name",
			Value: boolValue,
		})

		// YAML will coerce bool to string
		require.NoError(t, err, "YAML coerces bool to string")

		// When loaded as ProjectConfig, it gets coerced to string "true"
		reloadedConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
		require.NoError(t, err)
		require.Equal(t, "true", reloadedConfig.Name, "YAML unmarshals bool as string 'true'")
	})
}

// Test_ProjectService_EventDispatcherPreservation validates that EventDispatchers
// are preserved across configuration updates for both projects and services.
// This ensures that event handlers registered by azure.yaml hooks and azd extensions
// continue to work after configuration modifications.
func Test_ProjectService_EventDispatcherPreservation(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()

	azdContext := azdcontext.NewAzdContextWithDirectory(temp)

	// Step 1: Load project using lazy project config
	projectConfig := &project.ProjectConfig{
		Name: "test-project",
		Services: map[string]*project.ServiceConfig{
			"web": {
				Name:         "web",
				RelativePath: "./src/web",
				Host:         project.ContainerAppTarget,
				Language:     project.ServiceLanguageDotNet,
			},
			"api": {
				Name:         "api",
				RelativePath: "./src/api",
				Host:         project.ContainerAppTarget,
				Language:     project.ServiceLanguagePython,
			},
		},
	}

	// Save initial project configuration
	err := project.Save(*mockContext.Context, projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

	// Load project config to get proper initialization
	loadedConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
	require.NoError(t, err)

	// Setup lazy dependencies
	lazyAzdContext := lazy.From(azdContext)
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(
		mockContext.Container,
		azdContext,
		mockContext.Console,
		localDataStore,
		nil,
	)
	require.NoError(t, err)
	lazyEnvManager := lazy.From(envManager)
	lazyProjectConfig := lazy.From(loadedConfig)

	// Step 2: Register event handlers for project and services
	// EventDispatchers are already initialized by project.Load()
	projectEventCount := atomic.Int32{}
	webServiceEventCount := atomic.Int32{}
	apiServiceEventCount := atomic.Int32{}

	// Register project-level event handler
	err = loadedConfig.AddHandler(
		*mockContext.Context,
		project.ProjectEventDeploy,
		func(ctx context.Context, args project.ProjectLifecycleEventArgs) error {
			projectEventCount.Add(1)
			return nil
		},
	)
	require.NoError(t, err)

	// Register service-level event handlers
	err = loadedConfig.Services["web"].AddHandler(
		*mockContext.Context,
		project.ServiceEventDeploy,
		func(ctx context.Context, args project.ServiceLifecycleEventArgs) error {
			webServiceEventCount.Add(1)
			return nil
		},
	)
	require.NoError(t, err)

	err = loadedConfig.Services["api"].AddHandler(
		*mockContext.Context,
		project.ServiceEventDeploy,
		func(ctx context.Context, args project.ServiceLifecycleEventArgs) error {
			apiServiceEventCount.Add(1)
			return nil
		},
	)
	require.NoError(t, err)

	// Create project service
	importManager := project.NewImportManager(&project.DotNetImporter{})
	service := NewProjectService(lazyAzdContext, lazyEnvManager, lazyProjectConfig, importManager, nil)

	// Step 3: Modify project configuration
	customValue, err := structpb.NewValue("project-custom-value")
	require.NoError(t, err)

	_, err = service.SetConfigValue(*mockContext.Context, &azdext.SetProjectConfigValueRequest{
		Path:  "custom.setting",
		Value: customValue,
	})
	require.NoError(t, err)

	// Step 4: Modify service configuration (web)
	webCustomValue, err := structpb.NewValue("web-custom-value")
	require.NoError(t, err)

	_, err = service.SetServiceConfigValue(*mockContext.Context, &azdext.SetServiceConfigValueRequest{
		ServiceName: "web",
		Path:        "custom.endpoint",
		Value:       webCustomValue,
	})
	require.NoError(t, err)

	// Modify service configuration (api)
	apiCustomValue, err := structpb.NewValue("api-custom-value")
	require.NoError(t, err)

	_, err = service.SetServiceConfigValue(*mockContext.Context, &azdext.SetServiceConfigValueRequest{
		ServiceName: "api",
		Path:        "custom.port",
		Value:       apiCustomValue,
	})
	require.NoError(t, err)

	// Step 5: Get the updated project config from lazy loader to verify event dispatchers are preserved
	updatedConfig, err := lazyProjectConfig.GetValue()
	require.NoError(t, err)

	// The project config should be a NEW instance (reloaded from disk)
	require.NotSame(t, loadedConfig, updatedConfig, "project config should be a new instance after reload")

	// But the EventDispatchers should be the SAME instances (preserved pointers)
	require.Same(t, loadedConfig.EventDispatcher, updatedConfig.EventDispatcher,
		"project EventDispatcher should be the same instance (preserved)")
	require.Same(t, loadedConfig.Services["web"].EventDispatcher, updatedConfig.Services["web"].EventDispatcher,
		"web service EventDispatcher should be the same instance (preserved)")
	require.Same(t, loadedConfig.Services["api"].EventDispatcher, updatedConfig.Services["api"].EventDispatcher,
		"api service EventDispatcher should be the same instance (preserved)")

	// Verify event dispatchers are not nil
	require.NotNil(t, updatedConfig.EventDispatcher, "project EventDispatcher should be preserved")
	require.NotNil(
		t,
		updatedConfig.Services["web"].EventDispatcher,
		"web service EventDispatcher should be preserved",
	)
	require.NotNil(
		t,
		updatedConfig.Services["api"].EventDispatcher,
		"api service EventDispatcher should be preserved",
	)

	// Step 6: Invoke event handlers on project by raising the event directly
	err = updatedConfig.RaiseEvent(
		*mockContext.Context,
		project.ProjectEventDeploy,
		project.ProjectLifecycleEventArgs{
			Project: updatedConfig,
		},
	)
	require.NoError(t, err)

	// Step 7: Invoke event handlers on services by raising the events directly
	err = updatedConfig.Services["web"].RaiseEvent(
		*mockContext.Context,
		project.ServiceEventDeploy,
		project.ServiceLifecycleEventArgs{
			Project: updatedConfig,
			Service: updatedConfig.Services["web"],
		},
	)
	require.NoError(t, err)

	err = updatedConfig.Services["api"].RaiseEvent(
		*mockContext.Context,
		project.ServiceEventDeploy,
		project.ServiceLifecycleEventArgs{
			Project: updatedConfig,
			Service: updatedConfig.Services["api"],
		},
	)
	require.NoError(t, err)

	// Step 8: Validate event handlers were invoked
	require.Equal(t, int32(1), projectEventCount.Load(), "project event handler should be invoked once")
	require.Equal(t, int32(1), webServiceEventCount.Load(), "web service event handler should be invoked once")
	require.Equal(t, int32(1), apiServiceEventCount.Load(), "api service event handler should be invoked once")

	// Additional verification: Ensure configuration changes were persisted
	verifyResp, err := service.GetConfigValue(*mockContext.Context, &azdext.GetProjectConfigValueRequest{
		Path: "custom.setting",
	})
	require.NoError(t, err)
	require.True(t, verifyResp.Found)
	require.Equal(t, "project-custom-value", verifyResp.Value.GetStringValue())

	webVerifyResp, err := service.GetServiceConfigValue(*mockContext.Context, &azdext.GetServiceConfigValueRequest{
		ServiceName: "web",
		Path:        "custom.endpoint",
	})
	require.NoError(t, err)
	require.True(t, webVerifyResp.Found)
	require.Equal(t, "web-custom-value", webVerifyResp.Value.GetStringValue())

	apiVerifyResp, err := service.GetServiceConfigValue(*mockContext.Context, &azdext.GetServiceConfigValueRequest{
		ServiceName: "api",
		Path:        "custom.port",
	})
	require.NoError(t, err)
	require.True(t, apiVerifyResp.Found)
	require.Equal(t, "api-custom-value", apiVerifyResp.Value.GetStringValue())
}

// Test_ProjectService_EventDispatcherPreservation_MultipleUpdates tests that event dispatchers
// remain functional after multiple sequential configuration updates.
func Test_ProjectService_EventDispatcherPreservation_MultipleUpdates(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()

	azdContext := azdcontext.NewAzdContextWithDirectory(temp)

	projectConfig := &project.ProjectConfig{
		Name: "test-project",
		Services: map[string]*project.ServiceConfig{
			"web": {
				Name:         "web",
				RelativePath: "./src/web",
				Host:         project.ContainerAppTarget,
				Language:     project.ServiceLanguageDotNet,
			},
		},
	}

	err := project.Save(*mockContext.Context, projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

	loadedConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
	require.NoError(t, err)

	lazyAzdContext := lazy.From(azdContext)
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(
		mockContext.Container,
		azdContext,
		mockContext.Console,
		localDataStore,
		nil,
	)
	require.NoError(t, err)
	lazyEnvManager := lazy.From(envManager)
	lazyProjectConfig := lazy.From(loadedConfig)

	// Register event handler (EventDispatcher already initialized by project.Load())
	eventCount := atomic.Int32{}
	err = loadedConfig.AddHandler(
		*mockContext.Context,
		project.ProjectEventDeploy,
		func(ctx context.Context, args project.ProjectLifecycleEventArgs) error {
			eventCount.Add(1)
			return nil
		},
	)
	require.NoError(t, err)

	importManager := project.NewImportManager(&project.DotNetImporter{})
	service := NewProjectService(lazyAzdContext, lazyEnvManager, lazyProjectConfig, importManager, nil)

	// Perform multiple configuration updates
	for i := 1; i <= 3; i++ {
		value, err := structpb.NewValue(i)
		require.NoError(t, err)

		_, err = service.SetConfigValue(*mockContext.Context, &azdext.SetProjectConfigValueRequest{
			Path:  "custom.counter",
			Value: value,
		})
		require.NoError(t, err)
	}

	// Verify event dispatcher still works after multiple updates
	updatedConfig, err := lazyProjectConfig.GetValue()
	require.NoError(t, err)

	// The project config should be a NEW instance (reloaded from disk)
	require.NotSame(t, loadedConfig, updatedConfig, "project config should be a new instance after reload")

	// But the EventDispatcher should be the SAME instance (preserved pointer)
	require.Same(t, loadedConfig.EventDispatcher, updatedConfig.EventDispatcher,
		"project EventDispatcher should be the same instance (preserved)")
	require.NotNil(t, updatedConfig.EventDispatcher)

	err = updatedConfig.RaiseEvent(
		*mockContext.Context,
		project.ProjectEventDeploy,
		project.ProjectLifecycleEventArgs{Project: updatedConfig},
	)
	require.NoError(t, err)

	require.Equal(t, int32(1), eventCount.Load(), "event handler should be invoked after multiple config updates")
}

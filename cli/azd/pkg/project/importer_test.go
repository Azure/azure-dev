// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	_ "embed"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
)

func TestImportManagerHasService(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	mockEnv := &mockenv.MockEnvManager{}
	mockEnv.On("Save", mock.Anything, mock.Anything).Return(nil)

	manager := NewImportManager(&DotNetImporter{
		dotnetCli: dotnet.NewCli(mockContext.CommandRunner),
		console:   mockContext.Console,
		lazyEnv: lazy.NewLazy(func() (*environment.Environment, error) {
			return environment.NewWithValues("env", map[string]string{}), nil
		}),
		lazyEnvManager: lazy.NewLazy(func() (environment.Manager, error) {
			return mockEnv, nil
		}),
	})

	// has service
	r, e := manager.HasService(*mockContext.Context, &ProjectConfig{
		Services: map[string]*ServiceConfig{
			"test": {
				Name:     "test",
				Language: ServiceLanguageJava,
			},
		},
	}, "test")
	require.NoError(t, e)
	require.True(t, r)

	// has not
	r, e = manager.HasService(*mockContext.Context, &ProjectConfig{
		Services: map[string]*ServiceConfig{
			"test": {
				Name:     "test",
				Language: ServiceLanguageJava,
			},
		},
	}, "other")
	require.NoError(t, e)
	require.False(t, r)
}

func TestImportManagerHasServiceErrorNoMultipleServicesWithAppHost(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	mockEnv := &mockenv.MockEnvManager{}
	mockEnv.On("Save", mock.Anything, mock.Anything).Return(nil)

	manager := NewImportManager(&DotNetImporter{
		dotnetCli: dotnet.NewCli(mockContext.CommandRunner),
		console:   mockContext.Console,
		lazyEnv: lazy.NewLazy(func() (*environment.Environment, error) {
			return environment.NewWithValues("env", map[string]string{}), nil
		}),
		lazyEnvManager: lazy.NewLazy(func() (environment.Manager, error) {
			return mockEnv, nil
		}),
		hostCheck: make(map[string]hostCheckResult),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "dotnet") &&
			slices.Contains(args.Args, "--getProperty:IsAspireHost")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{
			Stdout:   aspireAppHostSniffResult,
			ExitCode: 0,
		}, nil
	})

	// errors ** errNoMultipleServicesWithAppHost **
	r, e := manager.HasService(*mockContext.Context, &ProjectConfig{
		Path: "path",
		Services: map[string]*ServiceConfig{
			"test": {
				Name:         "test",
				Language:     ServiceLanguageDotNet,
				RelativePath: "path",
				Project: &ProjectConfig{
					Path: "path",
				},
			},
			"foo": {
				Name:         "foo",
				Language:     ServiceLanguageDotNet,
				RelativePath: "path2",
				Project: &ProjectConfig{
					Path: "path",
				},
			},
		},
	}, "other")
	require.Error(t, e, errNoMultipleServicesWithAppHost)
	require.False(t, r)
}

func TestImportManagerHasServiceErrorAppHostMustTargetContainerApp(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	mockEnv := &mockenv.MockEnvManager{}
	mockEnv.On("Save", mock.Anything, mock.Anything).Return(nil)

	manager := NewImportManager(&DotNetImporter{
		dotnetCli: dotnet.NewCli(mockContext.CommandRunner),
		console:   mockContext.Console,
		lazyEnv: lazy.NewLazy(func() (*environment.Environment, error) {
			return environment.NewWithValues("env", map[string]string{}), nil
		}),
		lazyEnvManager: lazy.NewLazy(func() (environment.Manager, error) {
			return mockEnv, nil
		}),
		hostCheck: make(map[string]hostCheckResult),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "dotnet") &&
			slices.Contains(args.Args, "--getProperty:IsAspireHost")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{
			Stdout:   aspireAppHostSniffResult,
			ExitCode: 0,
		}, nil
	})

	// errors ** errNoMultipleServicesWithAppHost **
	r, e := manager.HasService(*mockContext.Context, &ProjectConfig{
		Path: "path",
		Services: map[string]*ServiceConfig{
			"test": {
				Name:         "test",
				Language:     ServiceLanguageDotNet,
				Host:         StaticWebAppTarget,
				RelativePath: "path",
				Project: &ProjectConfig{
					Path: "path",
				},
			},
		},
	}, "other")
	require.Error(t, e, errAppHostMustTargetContainerApp)
	require.False(t, r)
}

func TestImportManagerProjectInfrastructureDefaults(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	mockEnv := &mockenv.MockEnvManager{}
	mockEnv.On("Save", mock.Anything, mock.Anything).Return(nil)

	manager := NewImportManager(&DotNetImporter{
		dotnetCli: dotnet.NewCli(mockContext.CommandRunner),
		console:   mockContext.Console,
		lazyEnv: lazy.NewLazy(func() (*environment.Environment, error) {
			return environment.NewWithValues("env", map[string]string{}), nil
		}),
		lazyEnvManager: lazy.NewLazy(func() (environment.Manager, error) {
			return mockEnv, nil
		}),
		hostCheck:           make(map[string]hostCheckResult),
		alphaFeatureManager: mockContext.AlphaFeaturesManager,
	})

	// ProjectInfrastructure does defaulting when no infra exists (fallback path)
	r, e := manager.ProjectInfrastructure(*mockContext.Context, &ProjectConfig{})
	require.NoError(t, e)
	require.Equal(t, DefaultProvisioningOptions.Path, r.Options.Path)
	require.Equal(t, DefaultProvisioningOptions.Module, r.Options.Module)

	// adding infra folder to test defaults
	expectedDefaultFolder := DefaultProvisioningOptions.Path
	err := os.Mkdir(expectedDefaultFolder, os.ModePerm)
	require.NoError(t, err)
	defer os.RemoveAll(expectedDefaultFolder)

	// Create the file
	expectedDefaultModule := DefaultProvisioningOptions.Module
	path := filepath.Join(expectedDefaultFolder, expectedDefaultModule)
	err = os.WriteFile(path, []byte(""), 0600)
	require.NoError(t, err)
	defer os.Remove(path)

	// ProjectInfrastructure does defaulting when infra exists (short-circuit path)
	r, e = manager.ProjectInfrastructure(*mockContext.Context, &ProjectConfig{})
	require.NoError(t, e)
	require.Equal(t, expectedDefaultFolder, r.Options.Path)
	require.Equal(t, expectedDefaultModule, r.Options.Module)
}

func TestImportManagerProjectInfrastructure(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	mockEnv := &mockenv.MockEnvManager{}
	mockEnv.On("Save", mock.Anything, mock.Anything).Return(nil)

	manager := NewImportManager(&DotNetImporter{
		dotnetCli: dotnet.NewCli(mockContext.CommandRunner),
		console:   mockContext.Console,
		lazyEnv: lazy.NewLazy(func() (*environment.Environment, error) {
			return environment.NewWithValues("env", map[string]string{}), nil
		}),
		lazyEnvManager: lazy.NewLazy(func() (environment.Manager, error) {
			return mockEnv, nil
		}),
		hostCheck: make(map[string]hostCheckResult),
	})

	// Do not use defaults
	expectedDefaultFolder := "customFolder"
	expectedDefaultModule := "customModule"

	// adding infra folder to test defaults
	err := os.Mkdir(expectedDefaultFolder, os.ModePerm)
	require.NoError(t, err)
	defer os.RemoveAll(expectedDefaultFolder)

	// Create the file
	path := filepath.Join(expectedDefaultFolder, expectedDefaultModule)
	err = os.WriteFile(path, []byte(""), 0600)
	require.NoError(t, err)
	defer os.Remove(path)

	r, e := manager.ProjectInfrastructure(*mockContext.Context, &ProjectConfig{
		Infra: provisioning.Options{
			Path:   expectedDefaultFolder,
			Module: expectedDefaultModule,
		},
	})

	require.NoError(t, e)
	require.Equal(t, expectedDefaultFolder, r.Options.Path)
	require.Equal(t, expectedDefaultModule, r.Options.Module)
}

//go:embed testdata/aspire-simple.json
var aspireSimpleManifest []byte

func TestImportManagerProjectInfrastructureAspire(t *testing.T) {
	manifestInvokeCount := 0

	mockContext := mocks.NewMockContext(t.Context())
	mockEnv := &mockenv.MockEnvManager{}
	mockEnv.On("Save", mock.Anything, mock.Anything).Return(nil)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "dotnet") &&
			slices.Contains(args.Args, "--getProperty:IsAspireHost")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{
			Stdout:   aspireAppHostSniffResult,
			ExitCode: 0,
		}, nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "dotnet") &&
			slices.Contains(args.Args, "--publisher") &&
			slices.Contains(args.Args, "manifest")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		manifestInvokeCount++

		require.Contains(t, args.Env, "DOTNET_ENVIRONMENT=Development")

		err := os.WriteFile(args.Args[6], aspireSimpleManifest, osutil.PermissionFile)
		if err != nil {
			return exec.RunResult{
				ExitCode: -1,
				Stderr:   err.Error(),
			}, err
		}
		return exec.RunResult{}, nil
	})

	manager := NewImportManager(&DotNetImporter{
		dotnetCli: dotnet.NewCli(mockContext.CommandRunner),
		console:   mockContext.Console,
		lazyEnv: lazy.NewLazy(func() (*environment.Environment, error) {
			return environment.NewWithValues("env", map[string]string{
				"DOTNET_ENVIRONMENT": "Development",
			}), nil
		}),
		lazyEnvManager: lazy.NewLazy(func() (environment.Manager, error) {
			return mockEnv, nil
		}),
		hostCheck:           make(map[string]hostCheckResult),
		cache:               make(map[manifestCacheKey]*apphost.Manifest),
		alphaFeatureManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	})

	// adding infra folder to test defaults
	err := os.Mkdir(DefaultProvisioningOptions.Path, os.ModePerm)
	require.NoError(t, err)
	defer os.RemoveAll(DefaultProvisioningOptions.Path)

	// Simulating the scenario where a customer is using Aspire and has an infra folder with a module that is not the default
	path := filepath.Join(DefaultProvisioningOptions.Path, "customModule")
	err = os.WriteFile(path, []byte(""), 0600)
	require.NoError(t, err)
	defer os.Remove(path)

	// Use an a dotnet project and use the mock to simulate an Aspire project
	r, e := manager.ProjectInfrastructure(*mockContext.Context, &ProjectConfig{
		Services: map[string]*ServiceConfig{
			"test": {
				Name:         "test",
				Language:     ServiceLanguageDotNet,
				Host:         ContainerAppTarget,
				RelativePath: "path",
				Project: &ProjectConfig{
					Path: "path",
				},
			},
		},
	})

	require.NoError(t, e)
	require.Equal(t, 1, manifestInvokeCount)

	// dotnet_importer creates a temp path for the infrastructure.
	// We can't figure the exact path, but it should contain the `azd-infra` label in it
	require.Contains(t, r.Options.Path, "azd-infra")
	require.Equal(t, DefaultProvisioningOptions.Module, r.Options.Module)
	require.Equal(t, r.cleanupDir, r.Options.Path)

	// If we fetch the infrastructure again, we expect that the manifest is already cached and `dotnet run` on the apphost
	// will not be invoked again.

	// Use an a dotnet project and use the mock to simulate an Aspire project
	_, e = manager.ProjectInfrastructure(*mockContext.Context, &ProjectConfig{
		Services: map[string]*ServiceConfig{
			"test": {
				Name:         "test",
				Language:     ServiceLanguageDotNet,
				Host:         ContainerAppTarget,
				RelativePath: "path",
				Project: &ProjectConfig{
					Path: "path",
				},
			},
		},
	})

	require.NoError(t, e)
	require.Equal(t, 1, manifestInvokeCount)
}

const prjWithResources = `
name: myproject
resources:
  api:
    type: host.containerapp
    port: 80
    uses:
      - postgresdb
      - mongodb
      - redis
  web:
    type: host.containerapp
    port: 80
    uses:
    - api
  postgresdb:
    type: db.postgres
  mongodb:
    type: db.mongo
  redis:
    type: db.redis
`

func Test_ImportManager_ProjectInfrastructure_FromResources(t *testing.T) {
	im := &ImportManager{
		dotNetImporter: &DotNetImporter{
			alphaFeatureManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		},
	}

	prjConfig := &ProjectConfig{}
	err := yaml.Unmarshal([]byte(prjWithResources), prjConfig)
	require.NoError(t, err)

	infra, err := im.ProjectInfrastructure(t.Context(), prjConfig)
	assert.NoError(t, err)

	assert.NotNil(t, infra.cleanupDir, "should be a temp dir")

	dir := infra.Options.Path
	assert.FileExists(t, filepath.Join(dir, "main.bicep"))
	assert.FileExists(t, filepath.Join(dir, "main.parameters.json"))
	assert.FileExists(t, filepath.Join(dir, "resources.bicep"))
}

func TestImportManager_GenerateAllInfrastructure_FromResources(t *testing.T) {
	im := &ImportManager{
		dotNetImporter: &DotNetImporter{
			alphaFeatureManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		},
	}

	prjConfig := &ProjectConfig{}
	err := yaml.Unmarshal([]byte(prjWithResources), prjConfig)
	require.NoError(t, err)

	projectFs, err := im.GenerateAllInfrastructure(t.Context(), prjConfig)
	require.NoError(t, err)

	files := []string{
		"main.bicep",
		"main.parameters.json",
		"resources.bicep",
	}
	for _, f := range files {
		_, err := projectFs.Open(filepath.Join(DefaultProvisioningOptions.Path, f))
		assert.NoError(t, err, "file %s should exist", f)
	}
}

// aspireAppHostSniffResult is mock data that would be returned by `dotnet msbuild` when fetching information about an
// Aspire project. This is used to simulate the scenario where a project is an Aspire project. A real Aspire project would
// have many entries in the ProjectCapability array (unrelated to the Aspire capability), but most have been omitted for
// simplicity. An unrelated entry is included to ensure we are looking at the entire array of capabilities.
// nolint: lll
var aspireAppHostSniffResult string = `{
  "Properties": {
    "IsAspireHost": "true"
  },
  "Items": {
    "ProjectCapability": [
      {
        "Identity": "LocalUserSecrets",
        "FullPath": "/Users/matell/dd/ellismg/AspireBicep/AspireStarter/AspireStarter.AppHost/LocalUserSecrets",
        "RootDir": "/",
        "Filename": "LocalUserSecrets",
        "Extension": "",
        "RelativeDir": "",
        "Directory": "Users/matell/dd/ellismg/AspireBicep/AspireStarter/AspireStarter.AppHost/",
        "RecursiveDir": "",
        "ModifiedTime": "",
        "CreatedTime": "",
        "AccessedTime": "",
        "DefiningProjectFullPath": "/Users/matell/.nuget/packages/microsoft.extensions.configuration.usersecrets/8.0.0/buildTransitive/net6.0/Microsoft.Extensions.Configuration.UserSecrets.props",
        "DefiningProjectDirectory": "/Users/matell/.nuget/packages/microsoft.extensions.configuration.usersecrets/8.0.0/buildTransitive/net6.0/",
        "DefiningProjectName": "Microsoft.Extensions.Configuration.UserSecrets",
        "DefiningProjectExtension": ".props"
      },	
      {
        "Identity": "Aspire",
        "FullPath": "/Users/matell/dd/ellismg/AspireBicep/AspireStarter/AspireStarter.AppHost/Aspire",
        "RootDir": "/",
        "Filename": "Aspire",
        "Extension": "",
        "RelativeDir": "",
        "Directory": "Users/matell/dd/ellismg/AspireBicep/AspireStarter/AspireStarter.AppHost/",
        "RecursiveDir": "",
        "ModifiedTime": "",
        "CreatedTime": "",
        "AccessedTime": "",
        "DefiningProjectFullPath": "/Users/matell/.nuget/packages/aspire.hosting.apphost/8.2.0/build/Aspire.Hosting.AppHost.targets",
        "DefiningProjectDirectory": "/Users/matell/.nuget/packages/aspire.hosting.apphost/8.2.0/build/",
        "DefiningProjectName": "Aspire.Hosting.AppHost",
        "DefiningProjectExtension": ".targets"
      }
    ]
  }
}`

func TestImportManagerServiceStableWithDependencyOrdering(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	mockEnv := &mockenv.MockEnvManager{}
	mockEnv.On("Save", mock.Anything, mock.Anything).Return(nil)

	manager := NewImportManager(&DotNetImporter{
		dotnetCli: dotnet.NewCli(mockContext.CommandRunner),
		console:   mockContext.Console,
		lazyEnv: lazy.NewLazy(func() (*environment.Environment, error) {
			return environment.NewWithValues("env", map[string]string{}), nil
		}),
		lazyEnvManager: lazy.NewLazy(func() (environment.Manager, error) {
			return mockEnv, nil
		}),
	})

	tests := []struct {
		name               string
		services           map[string]*ServiceConfig
		resources          map[string]*ResourceConfig
		expectedVariations [][]string // All valid orderings (single slice for deterministic cases)
		shouldError        bool
		errorMsg           string
	}{
		{
			name: "no dependencies - alphabetical order maintained",
			services: map[string]*ServiceConfig{
				"zebra": {Name: "zebra", Uses: []string{}},
				"alpha": {Name: "alpha", Uses: []string{}},
				"beta":  {Name: "beta", Uses: []string{}},
			},
			expectedVariations: [][]string{
				{"alpha", "beta", "zebra"}, // Alphabetical order when no dependencies
			},
		},
		{
			name: "simple dependency chain",
			services: map[string]*ServiceConfig{
				"frontend": {Name: "frontend", Uses: []string{"backend"}},
				"backend":  {Name: "backend", Uses: []string{"database"}},
				"database": {Name: "database", Uses: []string{}},
			},
			expectedVariations: [][]string{
				{"database", "backend", "frontend"},
			},
		},
		{
			name: "complex dependencies",
			services: map[string]*ServiceConfig{
				"api":      {Name: "api", Uses: []string{"auth", "storage"}},
				"web":      {Name: "web", Uses: []string{"api"}},
				"auth":     {Name: "auth", Uses: []string{"database"}},
				"storage":  {Name: "storage", Uses: []string{"database"}},
				"database": {Name: "database", Uses: []string{}},
			},
			expectedVariations: [][]string{
				{"database", "auth", "storage", "api", "web"}, // Original expected order
				{"database", "storage", "auth", "api", "web"}, // Alternative valid order
			},
		},
		{
			name: "service depending on resource",
			services: map[string]*ServiceConfig{
				"api": {Name: "api", Uses: []string{"database"}},
				"web": {Name: "web", Uses: []string{"api"}},
			},
			resources: map[string]*ResourceConfig{
				"database": {Name: "database", Type: "db.postgres"},
			},
			expectedVariations: [][]string{
				{"api", "web"}, // Resource dependencies don't affect service ordering
			},
		},
		{
			name: "circular dependency",
			services: map[string]*ServiceConfig{
				"service1": {Name: "service1", Uses: []string{"service2"}},
				"service2": {Name: "service2", Uses: []string{"service1"}},
			},
			shouldError: true,
			errorMsg:    "circular dependency detected",
		},
		{
			name: "self dependency",
			services: map[string]*ServiceConfig{
				"api": {Name: "api", Uses: []string{"api"}},
			},
			shouldError: true,
			errorMsg:    "circular dependency detected",
		},
		{
			name: "missing dependency",
			services: map[string]*ServiceConfig{
				"api": {Name: "api", Uses: []string{"nonexistent"}},
			},
			shouldError: true,
			errorMsg:    "does not exist as a service or resource",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectConfig := &ProjectConfig{
				Services:  tt.services,
				Resources: tt.resources,
			}

			result, err := manager.ServiceStable(*mockContext.Context, projectConfig)

			if tt.shouldError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
				require.Len(t, result, len(tt.expectedVariations[0]))

				// Get the actual service names for comparison
				actualOrder := make([]string, len(result))
				for i, svc := range result {
					actualOrder[i] = svc.Name
				}

				// Check if the actual order matches any of the expected variations
				matchesAnyVariation := false
				for _, expectedVariation := range tt.expectedVariations {
					if slices.Equal(actualOrder, expectedVariation) {
						matchesAnyVariation = true
						break
					}
				}

				if !matchesAnyVariation {
					t.Errorf("Actual order %v does not match any expected variations: %v",
						actualOrder, tt.expectedVariations)
				}
			}
		})
	}
}

func TestImportManagerServiceStableValidation(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	mockEnv := &mockenv.MockEnvManager{}

	manager := NewImportManager(&DotNetImporter{
		dotnetCli: dotnet.NewCli(mockContext.CommandRunner),
		console:   mockContext.Console,
		lazyEnv: lazy.NewLazy(func() (*environment.Environment, error) {
			return environment.NewWithValues("env", map[string]string{}), nil
		}),
		lazyEnvManager: lazy.NewLazy(func() (environment.Manager, error) {
			return mockEnv, nil
		}),
	})

	tests := []struct {
		name      string
		services  map[string]*ServiceConfig
		resources map[string]*ResourceConfig
		expectErr bool
		errorMsg  string
	}{
		{
			name: "valid service dependencies",
			services: map[string]*ServiceConfig{
				"frontend": {Name: "frontend", Uses: []string{"backend"}},
				"backend":  {Name: "backend", Uses: []string{}},
			},
			expectErr: false,
		},
		{
			name: "valid resource dependencies",
			services: map[string]*ServiceConfig{
				"api": {Name: "api", Uses: []string{"database"}},
			},
			resources: map[string]*ResourceConfig{
				"database": {Name: "database", Type: "db.postgres"},
			},
			expectErr: false,
		},
		{
			name: "invalid dependency",
			services: map[string]*ServiceConfig{
				"api": {Name: "api", Uses: []string{"nonexistent"}},
			},
			expectErr: true,
			errorMsg:  "does not exist as a service or resource",
		},
		{
			name: "mixed valid and invalid dependencies",
			services: map[string]*ServiceConfig{
				"api":      {Name: "api", Uses: []string{"database", "nonexistent"}},
				"frontend": {Name: "frontend", Uses: []string{"api"}},
			},
			resources: map[string]*ResourceConfig{
				"database": {Name: "database", Type: "db.postgres"},
			},
			expectErr: true,
			errorMsg:  "does not exist as a service or resource",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectConfig := &ProjectConfig{
				Services:  tt.services,
				Resources: tt.resources,
			}

			_, err := manager.ServiceStable(*mockContext.Context, projectConfig)

			if tt.expectErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestImportManagerServiceStableWithDependencies(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	mockEnv := &mockenv.MockEnvManager{}
	mockEnv.On("Save", mock.Anything, mock.Anything).Return(nil)

	manager := NewImportManager(&DotNetImporter{
		dotnetCli: dotnet.NewCli(mockContext.CommandRunner),
		console:   mockContext.Console,
		lazyEnv: lazy.NewLazy(func() (*environment.Environment, error) {
			return environment.NewWithValues("env", map[string]string{}), nil
		}),
		lazyEnvManager: lazy.NewLazy(func() (environment.Manager, error) {
			return mockEnv, nil
		}),
	})

	// Test that ServiceStable returns services in dependency order
	projectConfig := &ProjectConfig{
		Services: map[string]*ServiceConfig{
			"frontend": {
				Name: "frontend",
				Uses: []string{"backend", "auth"},
			},
			"backend": {
				Name: "backend",
				Uses: []string{"database"},
			},
			"auth": {
				Name: "auth",
				Uses: []string{"database"},
			},
			"database": {
				Name: "database",
				Uses: []string{},
			},
		},
		Resources: map[string]*ResourceConfig{
			"storage": {Name: "storage", Type: "storage"},
		},
	}

	services, err := manager.ServiceStable(*mockContext.Context, projectConfig)
	require.NoError(t, err)
	require.Len(t, services, 4)

	// Verify dependency order: database should come first, frontend should come last
	serviceNames := make([]string, len(services))
	for i, svc := range services {
		serviceNames[i] = svc.Name
	}

	// Check that dependencies come before dependents
	databaseIdx := slices.Index(serviceNames, "database")
	backendIdx := slices.Index(serviceNames, "backend")
	authIdx := slices.Index(serviceNames, "auth")
	frontendIdx := slices.Index(serviceNames, "frontend")

	assert.True(t, databaseIdx < backendIdx, "database should come before backend")
	assert.True(t, databaseIdx < authIdx, "database should come before auth")
	assert.True(t, backendIdx < frontendIdx, "backend should come before frontend")
	assert.True(t, authIdx < frontendIdx, "auth should come before frontend")
}

func TestDetectProviderFromFiles(t *testing.T) {
	tests := []struct {
		name           string
		files          []string
		expectedResult provisioning.ProviderKind
		expectError    bool
		errorContains  string
	}{
		{
			name:           "only bicep files",
			files:          []string{"main.bicep", "modules.bicep"},
			expectedResult: provisioning.Bicep,
			expectError:    false,
		},
		{
			name:           "only bicepparam files",
			files:          []string{"main.bicepparam"},
			expectedResult: provisioning.Bicep,
			expectError:    false,
		},
		{
			name:           "only terraform files",
			files:          []string{"main.tf", "variables.tf"},
			expectedResult: provisioning.Terraform,
			expectError:    false,
		},
		{
			name:           "only tfvars files",
			files:          []string{"terraform.tfvars"},
			expectedResult: provisioning.Terraform,
			expectError:    false,
		},
		{
			name:           "both bicep and terraform files",
			files:          []string{"main.bicep", "main.tf"},
			expectedResult: provisioning.NotSpecified,
			expectError:    true,
			errorContains:  "both Bicep and Terraform files detected",
		},
		{
			name:           "no IaC files",
			files:          []string{"readme.md", "config.json"},
			expectedResult: provisioning.NotSpecified,
			expectError:    false,
		},
		{
			name:           "empty directory",
			files:          []string{},
			expectedResult: provisioning.NotSpecified,
			expectError:    false,
		},
		{
			name:           "mixed with bicep and non-IaC files",
			files:          []string{"main.bicep", "readme.md", "config.json"},
			expectedResult: provisioning.Bicep,
			expectError:    false,
		},
		{
			name:           "mixed with terraform and non-IaC files",
			files:          []string{"main.tf", "readme.md", "LICENSE"},
			expectedResult: provisioning.Terraform,
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory
			tmpDir, err := os.MkdirTemp("", "test-detect-provider-*")
			require.NoError(t, err)
			defer os.RemoveAll(tmpDir)

			// Create test files
			for _, fileName := range tt.files {
				filePath := filepath.Join(tmpDir, fileName)
				err := os.WriteFile(filePath, []byte("test content"), 0600)
				require.NoError(t, err)
			}

			// Test detectProviderFromFiles
			result, err := detectProviderFromFiles(tmpDir)

			if tt.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errorContains)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestDetectProviderFromFilesNonExistentDirectory(t *testing.T) {
	// Test with non-existent directory
	result, err := detectProviderFromFiles("/nonexistent/path/that/does/not/exist")
	require.NoError(t, err, "should not error when directory doesn't exist")
	require.Equal(t, provisioning.NotSpecified, result)
}

func TestDetectProviderFromFilesIgnoresDirectories(t *testing.T) {
	// Create temporary directory structure
	tmpDir, err := os.MkdirTemp("", "test-detect-provider-dirs-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create subdirectories with IaC-like names
	err = os.Mkdir(filepath.Join(tmpDir, "main.bicep"), 0755)
	require.NoError(t, err)
	err = os.Mkdir(filepath.Join(tmpDir, "main.tf"), 0755)
	require.NoError(t, err)

	// Create a real Bicep file
	err = os.WriteFile(filepath.Join(tmpDir, "resources.bicep"), []byte("test"), 0600)
	require.NoError(t, err)

	// Should detect Bicep and ignore directories
	result, err := detectProviderFromFiles(tmpDir)
	require.NoError(t, err)
	require.Equal(t, provisioning.Bicep, result)
}

func Test_DetectProviderFromFiles(t *testing.T) {
	t.Run("empty directory", func(t *testing.T) {
		dir := t.TempDir()
		provider, err := detectProviderFromFiles(dir)
		require.NoError(t, err)
		assert.Empty(t, string(provider))
	})

	t.Run("non-existent directory", func(t *testing.T) {
		provider, err := detectProviderFromFiles(filepath.Join(t.TempDir(), "nonexistent"))
		require.NoError(t, err)
		assert.Empty(t, string(provider))
	})

	t.Run("bicep files only", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.bicep"), []byte("param x string"), 0600))

		provider, err := detectProviderFromFiles(dir)
		require.NoError(t, err)
		assert.Equal(t, "bicep", string(provider))
	})

	t.Run("terraform files only", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte("resource {}"), 0600))

		provider, err := detectProviderFromFiles(dir)
		require.NoError(t, err)
		assert.Equal(t, "terraform", string(provider))
	})

	t.Run("both bicep and terraform", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.bicep"), []byte("param x string"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte("resource {}"), 0600))

		_, err := detectProviderFromFiles(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "both Bicep and Terraform")
	})

	t.Run("bicepparam files", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.bicepparam"), []byte("param x = 'val'"), 0600))

		provider, err := detectProviderFromFiles(dir)
		require.NoError(t, err)
		assert.Equal(t, "bicep", string(provider))
	})

	t.Run("tfvars files", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "dev.tfvars"), []byte("x = \"val\""), 0600))

		provider, err := detectProviderFromFiles(dir)
		require.NoError(t, err)
		assert.Equal(t, "terraform", string(provider))
	})

	t.Run("directories are ignored", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "modules.bicep"), 0755))

		provider, err := detectProviderFromFiles(dir)
		require.NoError(t, err)
		assert.Empty(t, string(provider))
	})
}

func Test_PathHasModule(t *testing.T) {
	t.Run("bicep module exists", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.bicep"), []byte("param x string"), 0600))

		exists, err := pathHasModule(dir, "main")
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("terraform module exists", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte("resource {}"), 0600))

		exists, err := pathHasModule(dir, "main")
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("module does not exist", func(t *testing.T) {
		dir := t.TempDir()
		exists, err := pathHasModule(dir, "main")
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("non-existent path", func(t *testing.T) {
		_, err := pathHasModule(filepath.Join(t.TempDir(), "nonexistent"), "main")
		require.Error(t, err)
	})
}

func Test_Infra_Cleanup(t *testing.T) {
	t.Run("with cleanup dir", func(t *testing.T) {
		dir := t.TempDir()
		cleanupDir := filepath.Join(dir, "to-clean")
		require.NoError(t, os.MkdirAll(cleanupDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(cleanupDir, "file.txt"), []byte("data"), 0600))

		infra := &Infra{cleanupDir: cleanupDir}
		err := infra.Cleanup()
		require.NoError(t, err)

		_, err = os.Stat(cleanupDir)
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("without cleanup dir", func(t *testing.T) {
		infra := &Infra{}
		err := infra.Cleanup()
		require.NoError(t, err)
	})
}

func Test_ValidateServiceDependencies(t *testing.T) {
	im := NewImportManager(nil)

	t.Run("no dependencies", func(t *testing.T) {
		prj := &ProjectConfig{Name: "test"}
		services := []*ServiceConfig{
			{Name: "web"},
			{Name: "api"},
		}
		err := im.validateServiceDependencies(services, prj)
		require.NoError(t, err)
	})

	t.Run("valid service dependency", func(t *testing.T) {
		prj := &ProjectConfig{Name: "test"}
		services := []*ServiceConfig{
			{Name: "web", Uses: []string{"api"}},
			{Name: "api"},
		}
		err := im.validateServiceDependencies(services, prj)
		require.NoError(t, err)
	})

	t.Run("valid resource dependency", func(t *testing.T) {
		prj := &ProjectConfig{
			Name: "test",
			Resources: map[string]*ResourceConfig{
				"mydb": {Type: ResourceTypeDbPostgres, Name: "mydb"},
			},
		}
		services := []*ServiceConfig{
			{Name: "web", Uses: []string{"mydb"}},
		}
		err := im.validateServiceDependencies(services, prj)
		require.NoError(t, err)
	})

	t.Run("missing dependency", func(t *testing.T) {
		prj := &ProjectConfig{Name: "test"}
		services := []*ServiceConfig{
			{Name: "web", Uses: []string{"missing-service"}},
		}
		err := im.validateServiceDependencies(services, prj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing-service")
	})
}

func Test_SortServicesByDependencies(t *testing.T) {
	prj := &ProjectConfig{
		Name: "test",
		Services: map[string]*ServiceConfig{
			"web":    {Name: "web", Uses: []string{"api"}},
			"api":    {Name: "api"},
			"worker": {Name: "worker", Uses: []string{"api"}},
		},
	}
	prj.Services["web"].Project = prj
	prj.Services["api"].Project = prj
	prj.Services["worker"].Project = prj

	im := NewImportManager(nil)
	services := []*ServiceConfig{prj.Services["web"], prj.Services["api"], prj.Services["worker"]}

	sorted, err := im.sortServicesByDependencies(services, prj)
	require.NoError(t, err)

	// api should come before web and worker since they depend on it
	apiIdx := -1
	webIdx := -1
	workerIdx := -1
	for i, svc := range sorted {
		switch svc.Name {
		case "api":
			apiIdx = i
		case "web":
			webIdx = i
		case "worker":
			workerIdx = i
		}
	}
	assert.True(t, apiIdx < webIdx, "api should be before web")
	assert.True(t, apiIdx < workerIdx, "api should be before worker")
}

// Tests for Infra.Cleanup

// The directory should be removed

func Test_HasAppHost(t *testing.T) {
	t.Run("NoServices", func(t *testing.T) {
		im := NewImportManager(nil)
		pc := &ProjectConfig{
			Services: map[string]*ServiceConfig{},
		}

		result := im.HasAppHost(t.Context(), pc)
		assert.False(t, result)
	})

	t.Run("NonDotNetService", func(t *testing.T) {
		im := NewImportManager(nil)
		pc := &ProjectConfig{
			Services: map[string]*ServiceConfig{
				"web": {
					Name:     "web",
					Language: ServiceLanguagePython,
				},
			},
		}

		result := im.HasAppHost(t.Context(), pc)
		assert.False(t, result)
	})
}

func Test_NewImportManager(t *testing.T) {
	im := NewImportManager(nil)
	require.NotNil(t, im)

	env := environment.NewWithValues("test", nil)
	_ = env
}

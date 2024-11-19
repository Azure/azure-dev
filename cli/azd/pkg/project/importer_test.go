// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	_ "embed"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestImportManagerHasService(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
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
	}, mockinput.NewMockConsole())

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
	mockContext := mocks.NewMockContext(context.Background())
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
	}, mockinput.NewMockConsole())

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
	mockContext := mocks.NewMockContext(context.Background())
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
	}, mockinput.NewMockConsole())

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
	mockContext := mocks.NewMockContext(context.Background())
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
	}, mockinput.NewMockConsole())

	// Get defaults and error b/c no infra found and no Aspire project
	r, e := manager.ProjectInfrastructure(*mockContext.Context, &ProjectConfig{})
	require.NoError(t, e, "this project does not contain expected infrastructure")
	require.NotNil(t, r)
	require.Equal(t, r, &Infra{})

	// adding infra folder to test defaults
	expectedDefaultFolder := DefaultPath
	err := os.Mkdir(expectedDefaultFolder, os.ModePerm)
	require.NoError(t, err)
	defer os.RemoveAll(expectedDefaultFolder)

	// error should keep happening b/c infra folder exists but module is not found
	r, e = manager.ProjectInfrastructure(*mockContext.Context, &ProjectConfig{})
	require.NoError(t, e)
	require.NotNil(t, r)
	require.Equal(t, r, &Infra{})

	// Create the file
	expectedDefaultModule := DefaultModule
	path := filepath.Join(expectedDefaultFolder, expectedDefaultModule)
	err = os.WriteFile(path, []byte(""), 0600)
	require.NoError(t, err)
	defer os.Remove(path)

	// infra result should be returned now that the default values are found
	r, e = manager.ProjectInfrastructure(*mockContext.Context, &ProjectConfig{})
	require.NoError(t, e)
	require.Equal(t, expectedDefaultFolder, r.Options.Path)
	require.Equal(t, expectedDefaultModule, r.Options.Module)
}

func TestImportManagerProjectInfrastructure(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
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
	}, mockinput.NewMockConsole())

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

	mockContext := mocks.NewMockContext(context.Background())
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
	}, mockinput.NewMockConsole())

	// adding infra folder to test defaults
	err := os.Mkdir(DefaultPath, os.ModePerm)
	require.NoError(t, err)
	defer os.RemoveAll(DefaultPath)

	// Simulating the scenario where a customer is using Aspire and has an infra folder with a module that is not the default
	path := filepath.Join(DefaultPath, "customModule")
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
	require.Equal(t, DefaultModule, r.Options.Module)
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
	alpha.SetDefaultEnablement(string(featureCompose), true)
	t.Cleanup(func() { alpha.SetDefaultEnablement(string(featureCompose), false) })

	im := &ImportManager{
		dotNetImporter: &DotNetImporter{
			alphaFeatureManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		},
	}

	prjConfig := &ProjectConfig{}
	err := yaml.Unmarshal([]byte(prjWithResources), prjConfig)
	require.NoError(t, err)

	infra, err := im.ProjectInfrastructure(context.Background(), prjConfig)
	assert.NoError(t, err)

	assert.NotNil(t, infra.cleanupDir, "should be a temp dir")

	dir := infra.Options.Path
	assert.FileExists(t, filepath.Join(dir, "main.bicep"))
	assert.FileExists(t, filepath.Join(dir, "main.parameters.json"))
	assert.FileExists(t, filepath.Join(dir, "resources.bicep"))

	// Disable the alpha feature and check that an error is returned
	alpha.SetDefaultEnablement(string(featureCompose), false)

	_, err = im.ProjectInfrastructure(context.Background(), prjConfig)
	assert.Error(t, err)
}

func TestImportManager_SynthAllInfrastructure_FromResources(t *testing.T) {
	alpha.SetDefaultEnablement(string(featureCompose), true)
	t.Cleanup(func() { alpha.SetDefaultEnablement(string(featureCompose), false) })

	im := &ImportManager{
		dotNetImporter: &DotNetImporter{
			alphaFeatureManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		},
	}

	prjConfig := &ProjectConfig{}
	err := yaml.Unmarshal([]byte(prjWithResources), prjConfig)
	require.NoError(t, err)

	projectFs, err := im.SynthAllInfrastructure(context.Background(), prjConfig)
	require.NoError(t, err)

	files := []string{
		"main.bicep",
		"main.parameters.json",
		"resources.bicep",
	}
	for _, f := range files {
		_, err := projectFs.Open(filepath.Join(DefaultPath, f))
		assert.NoError(t, err, "file %s should exist", f)
	}

	// Disable the alpha feature
	alpha.SetDefaultEnablement(string(featureCompose), false)

	_, err = im.SynthAllInfrastructure(context.Background(), prjConfig)
	assert.Error(t, err)
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

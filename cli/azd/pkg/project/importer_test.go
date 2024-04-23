// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	_ "embed"
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
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestImportManagerHasService(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockEnv := &mockenv.MockEnvManager{}
	mockEnv.On("Save", mock.Anything, mock.Anything).Return(nil)

	manager := NewImportManager(&DotNetImporter{
		dotnetCli: dotnet.NewDotNetCli(mockContext.CommandRunner),
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
	mockContext := mocks.NewMockContext(context.Background())
	mockEnv := &mockenv.MockEnvManager{}
	mockEnv.On("Save", mock.Anything, mock.Anything).Return(nil)

	manager := NewImportManager(&DotNetImporter{
		dotnetCli: dotnet.NewDotNetCli(mockContext.CommandRunner),
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
			Stdout:   "true",
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
		dotnetCli: dotnet.NewDotNetCli(mockContext.CommandRunner),
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
			Stdout:   "true",
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
		dotnetCli: dotnet.NewDotNetCli(mockContext.CommandRunner),
		console:   mockContext.Console,
		lazyEnv: lazy.NewLazy(func() (*environment.Environment, error) {
			return environment.NewWithValues("env", map[string]string{}), nil
		}),
		lazyEnvManager: lazy.NewLazy(func() (environment.Manager, error) {
			return mockEnv, nil
		}),
		hostCheck: make(map[string]hostCheckResult),
	})

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
		dotnetCli: dotnet.NewDotNetCli(mockContext.CommandRunner),
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

//go:embed testdata/aspire-escaping.json
var aspireEscapingManifest []byte

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
			Stdout:   "true",
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

		err := os.WriteFile(args.Args[6], aspireEscapingManifest, osutil.PermissionFile)
		if err != nil {
			return exec.RunResult{
				ExitCode: -1,
				Stderr:   err.Error(),
			}, err
		}
		return exec.RunResult{}, nil
	})

	manager := NewImportManager(&DotNetImporter{
		dotnetCli: dotnet.NewDotNetCli(mockContext.CommandRunner),
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

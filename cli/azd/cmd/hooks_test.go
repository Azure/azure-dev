// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_HooksRunAction_RunsLayerHooks(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	env := environment.NewWithValues("test", nil)
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Reload", mock.Anything, mock.Anything).Return(nil)

	projectPath := t.TempDir()
	absoluteLayerPath := filepath.Join(t.TempDir(), "shared")

	projectConfig := &project.ProjectConfig{
		Name:     "test",
		Path:     projectPath,
		Services: map[string]*project.ServiceConfig{},
		Infra: provisioning.Options{
			Layers: []provisioning.Options{
				{
					Name: "core",
					Path: "infra/core",
					Hooks: provisioning.HooksConfig{
						"preprovision": {{
							Shell: ext.ShellTypeBash,
							Run:   "echo core",
						}},
					},
				},
				{
					Name: "shared",
					Path: absoluteLayerPath,
					Hooks: provisioning.HooksConfig{
						"preprovision": {{
							Shell: ext.ShellTypeBash,
							Run:   "echo shared",
						}},
					},
				},
			},
		},
	}

	var gotCwds []string
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return true
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		gotCwds = append(gotCwds, args.Cwd)
		return exec.NewRunResult(0, "", ""), nil
	})

	action := &hooksRunAction{
		projectConfig:  projectConfig,
		env:            env,
		envManager:     envManager,
		importManager:  project.NewImportManager(nil),
		commandRunner:  mockContext.CommandRunner,
		console:        mockContext.Console,
		flags:          &hooksRunFlags{},
		args:           []string{"preprovision"},
		serviceLocator: mockContext.Container,
	}

	result, err := action.Run(*mockContext.Context)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, []string{
		filepath.Join(projectPath, "infra/core"),
		absoluteLayerPath,
	}, gotCwds)
}

func Test_HooksRunAction_FiltersLayerHooks(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	env := environment.NewWithValues("test", nil)
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Reload", mock.Anything, mock.Anything).Return(nil)

	projectPath := t.TempDir()

	projectConfig := &project.ProjectConfig{
		Name:     "test",
		Path:     projectPath,
		Services: map[string]*project.ServiceConfig{},
		Infra: provisioning.Options{
			Layers: []provisioning.Options{
				{
					Name: "core",
					Path: "infra/core",
					Hooks: provisioning.HooksConfig{
						"preprovision": {{
							Shell: ext.ShellTypeBash,
							Run:   "echo core",
						}},
					},
				},
				{
					Name: "shared",
					Path: "infra/shared",
					Hooks: provisioning.HooksConfig{
						"preprovision": {{
							Shell: ext.ShellTypeBash,
							Run:   "echo shared",
						}},
					},
				},
			},
		},
	}

	var gotCwds []string
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return true
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		gotCwds = append(gotCwds, args.Cwd)
		return exec.NewRunResult(0, "", ""), nil
	})

	action := &hooksRunAction{
		projectConfig:  projectConfig,
		env:            env,
		envManager:     envManager,
		importManager:  project.NewImportManager(nil),
		commandRunner:  mockContext.CommandRunner,
		console:        mockContext.Console,
		flags:          &hooksRunFlags{layer: "shared"},
		args:           []string{"preprovision"},
		serviceLocator: mockContext.Container,
	}

	result, err := action.Run(*mockContext.Context)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, []string{
		filepath.Join(projectPath, "infra/shared"),
	}, gotCwds)
}

func Test_HooksRunAction_SetsTelemetryTypeForLayer(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	env := environment.NewWithValues("test", nil)
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Reload", mock.Anything, mock.Anything).Return(nil)

	t.Cleanup(func() {
		tracing.SetUsageAttributes()
	})
	tracing.SetUsageAttributes()

	projectConfig := &project.ProjectConfig{
		Name:     "test",
		Path:     t.TempDir(),
		Services: map[string]*project.ServiceConfig{},
		Infra: provisioning.Options{
			Layers: []provisioning.Options{
				{
					Name: "core",
					Path: "infra/core",
					Hooks: provisioning.HooksConfig{
						"preprovision": {{
							Shell: ext.ShellTypeBash,
							Run:   "echo core",
						}},
					},
				},
			},
		},
	}

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return true
	}).Respond(exec.NewRunResult(0, "", ""))

	action := &hooksRunAction{
		projectConfig:  projectConfig,
		env:            env,
		envManager:     envManager,
		importManager:  project.NewImportManager(nil),
		commandRunner:  mockContext.CommandRunner,
		console:        mockContext.Console,
		flags:          &hooksRunFlags{layer: "core"},
		args:           []string{"preprovision"},
		serviceLocator: mockContext.Container,
	}

	_, err := action.Run(*mockContext.Context)
	require.NoError(t, err)

	var hookType string
	for _, attr := range tracing.GetUsageAttributes() {
		if attr.Key == fields.HooksTypeKey.Key {
			hookType = attr.Value.AsString()
			break
		}
	}

	require.Equal(t, "layer", hookType)
}

func Test_HooksRunAction_RejectsServiceAndLayerTogether(t *testing.T) {
	action := &hooksRunAction{
		env:   environment.NewWithValues("test", nil),
		flags: &hooksRunFlags{service: "api", layer: "core"},
		args:  []string{"preprovision"},
	}

	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.ErrorContains(t, err, "--service and --layer cannot be used together")
}

func Test_HooksRunAction_ValidatesLayerHooksRelativeToLayerPath(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	env := environment.NewWithValues("test", nil)

	projectPath := t.TempDir()
	layerScriptPath := filepath.Join(projectPath, "infra", "core", "scripts", "preprovision.sh")
	require.NoError(t, os.MkdirAll(filepath.Dir(layerScriptPath), 0o755))
	require.NoError(t, os.WriteFile(layerScriptPath, []byte("echo pre"), 0o600))

	layerHook := &ext.HookConfig{
		Run: filepath.Join("scripts", "preprovision.sh"),
	}

	projectConfig := &project.ProjectConfig{
		Name:     "test",
		Path:     projectPath,
		Services: map[string]*project.ServiceConfig{},
		Infra: provisioning.Options{
			Layers: []provisioning.Options{
				{
					Name: "core",
					Path: filepath.Join("infra", "core"),
					Hooks: provisioning.HooksConfig{
						"preprovision": {layerHook},
					},
				},
			},
		},
	}

	action := &hooksRunAction{
		projectConfig:  projectConfig,
		env:            env,
		importManager:  project.NewImportManager(nil),
		commandRunner:  mockContext.CommandRunner,
		console:        mockContext.Console,
		flags:          &hooksRunFlags{},
		serviceLocator: mockContext.Container,
	}

	err := action.validateAndWarnHooks(*mockContext.Context)
	require.NoError(t, err)
	require.False(t, layerHook.IsUsingDefaultShell())
	require.Equal(t, ext.ScriptTypeUnknown, layerHook.Shell)
}

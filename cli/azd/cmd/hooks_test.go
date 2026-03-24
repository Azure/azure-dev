// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"path/filepath"
	"testing"

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

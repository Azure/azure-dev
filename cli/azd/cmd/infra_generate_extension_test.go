// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestInfraGenerateDoesNotStartAllExtensionListeners(t *testing.T) {
	root := actions.NewActionDescriptor("root", nil)
	for _, action := range infraActions(root).Children() {
		if action.Name != "generate" {
			continue
		}
		for _, registration := range action.Middleware() {
			require.NotEqual(t, "extensions", registration.Name)
		}
		return
	}
	t.Fatal("infra generate action was not registered")
}

type actionGenerationActivator struct {
	ensure func(context.Context, []string, string) (func(), error)
}

func (a *actionGenerationActivator) EnsureInfraGenerationProviders(
	ctx context.Context, names []string, environmentName string,
) (func(), error) {
	if a.ensure != nil {
		return a.ensure(ctx, names, environmentName)
	}
	return func() {}, nil
}

type actionInfrastructureGenerator struct {
	generate func(context.Context, *v1beta.InfraGenerateRequest) (*v1beta.InfraGenerateResponse, error)
}

func (g *actionInfrastructureGenerator) Generate(
	ctx context.Context, request *v1beta.InfraGenerateRequest,
) (*v1beta.InfraGenerateResponse, error) {
	return g.generate(ctx, request)
}

func generationExtensionAction(
	t *testing.T, generator *actionInfrastructureGenerator,
) (*infraGenerateAction, string) {
	t.Helper()
	const source = "name: example\ninfra:\n  layers:\n    - name: data\n      provider: resources\n"
	return generationExtensionActionWithSource(t, generator, source)
}

func generationExtensionActionWithSource(
	t *testing.T, generator *actionInfrastructureGenerator, source string,
) (*infraGenerateAction, string) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.yaml"), []byte(source), 0o600))
	config, err := project.Parse(t.Context(), source)
	require.NoError(t, err)
	config.Path = dir
	container := ioc.NewNestedContainer(nil)
	require.NoError(t, container.RegisterNamedTransient(project.InfraGenerationProviderKey("resources"),
		func() project.InfrastructureGenerator { return generator }))
	azdCtx := azdcontext.NewAzdContextWithDirectory(dir)
	return &infraGenerateAction{
		projectConfig:      config,
		generationManager:  project.NewInfraGenerationManager(project.NewImportManager(nil), container, azdCtx),
		extensionActivator: &actionGenerationActivator{},
		azdCtx:             azdCtx,
		flags:              &infraGenerateFlags{force: true, EnvFlag: &internal.EnvFlag{}},
		console:            mockinput.NewMockConsole(),
	}, dir
}

func TestInfraGenerateExtensionOwnsFileWrites(t *testing.T) {
	generator := &actionInfrastructureGenerator{}
	action, dir := generationExtensionAction(t, generator)
	generator.generate = func(
		_ context.Context, request *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		require.True(t, request.Options.Force)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "custom"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "custom", "main.tf"), []byte("extension source"), 0o600))
		return &v1beta.InfraGenerateResponse{Entry: &v1beta.ProvisioningOptions{
			Provider: "terraform", Path: "custom",
		}}, nil
	}
	_, err := action.Run(t.Context())
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(dir, "custom/main.tf"))
	require.NoError(t, err)
	require.Equal(t, "extension source", string(data))
	require.NoDirExists(t, filepath.Join(dir, "infra"), "core must not create its own output directory")
	require.Equal(t, provisioning.Terraform, action.projectConfig.Infra.Layers[0].Provider)
}

func TestInfraGenerateExtensionUsesResolvedAzureYml(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	projectDir, err := os.Getwd()
	require.NoError(t, err)
	projectFile := filepath.Join(dir, "azure.yml")
	source := "name: example\ninfra:\n  layers:\n    - name: data\n      provider: resources\n"
	require.NoError(t, os.WriteFile(projectFile, []byte(source), 0o600))

	container := ioc.NewNestedContainer(nil)
	ioc.RegisterInstance(container, t.Context())
	ioc.RegisterInstance(container, &internal.GlobalCommandOptions{})
	ioc.RegisterInstance(container, &cobra.Command{})
	registerCommonDependencies(container)

	called := false
	generator := &actionInfrastructureGenerator{generate: func(
		_ context.Context, request *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		called = true
		require.Equal(t, projectDir, request.ProjectPath)
		return &v1beta.InfraGenerateResponse{Entry: &v1beta.ProvisioningOptions{
			Provider: "bicep", Path: request.OutputPath, Module: "main",
		}}, nil
	}}
	require.NoError(t, container.RegisterNamedTransient(project.InfraGenerationProviderKey("resources"),
		func() project.InfrastructureGenerator { return generator }))

	var config *project.ProjectConfig
	require.NoError(t, container.Resolve(&config))
	var manager *project.InfraGenerationManager
	require.NoError(t, container.Resolve(&manager))
	var azdCtx *azdcontext.AzdContext
	require.NoError(t, container.Resolve(&azdCtx))

	action := &infraGenerateAction{
		projectConfig:      config,
		generationManager:  manager,
		extensionActivator: &actionGenerationActivator{},
		azdCtx:             azdCtx,
		flags:              &infraGenerateFlags{force: true, EnvFlag: &internal.EnvFlag{}},
		console:            mockinput.NewMockConsole(),
	}
	_, err = action.Run(t.Context())
	require.NoError(t, err)
	require.True(t, called)
	require.Equal(t, provisioning.Bicep, config.Infra.Layers[0].Provider)
	data, err := os.ReadFile(projectFile)
	require.NoError(t, err)
	saved, err := project.Parse(t.Context(), string(data))
	require.NoError(t, err)
	require.Equal(t, provisioning.Bicep, saved.Infra.Layers[0].Provider)
	require.NoFileExists(t, filepath.Join(dir, "azure.yaml"))
}

func TestInfraGenerateActivatesOnlyConfiguredGenerators(t *testing.T) {
	active, cleaned := false, false
	generator := &actionInfrastructureGenerator{}
	action, _ := generationExtensionAction(t, generator)
	action.flags.EnvironmentName = "test-env"
	action.flags.force = false
	action.extensionActivator = &actionGenerationActivator{
		ensure: func(_ context.Context, names []string, environmentName string) (func(), error) {
			require.Equal(t, []string{"resources"}, names)
			require.Equal(t, "test-env", environmentName)
			active = true
			return func() { active, cleaned = false, true }, nil
		},
	}
	generator.generate = func(
		_ context.Context, request *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		require.True(t, active)
		require.False(t, request.Options.Force)
		return &v1beta.InfraGenerateResponse{Entry: &v1beta.ProvisioningOptions{
			Provider: "bicep", Path: request.OutputPath, Module: "main",
		}}, nil
	}
	_, err := action.Run(t.Context())
	require.NoError(t, err)
	require.True(t, cleaned)
	require.False(t, active)
}

func TestInfraGenerateExtensionPreflightErrorsBeforeOverwrite(t *testing.T) {
	tests := []struct {
		name        string
		entry       string
		expectedErr string
	}{
		{
			name:        "unsupported provider",
			entry:       "      provider: unsupported\n      path: infra/data\n",
			expectedErr: `infrastructure provider "unsupported" does not have an available infra-generation-provider`,
		},
		{
			name:        "invalid output path",
			entry:       "      provider: resources\n      path: .\n",
			expectedErr: `output path "." must be project-relative`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A generator call would panic, as would an unexpected confirmation.
			const header = "name: example\ninfra:\n  layers:\n    - name: valid\n      provider: resources\n" +
				"    - name: data\n"
			action, dir := generationExtensionActionWithSource(t, &actionInfrastructureGenerator{}, header+tt.entry)
			action.flags.force = false
			console := mockinput.NewMockConsole()
			console.SetNoPromptMode(true)
			action.console = console
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra", "data"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "infra", "data", "notes.txt"), []byte("x"), 0o600))

			_, err := action.Run(t.Context())
			require.ErrorContains(t, err, tt.expectedErr)
			_, overwriteSuggestion := errors.AsType[*internal.ErrorWithSuggestion](err)
			require.False(t, overwriteSuggestion)
		})
	}
}

func TestInfraGenerateNonEmptyExtensionFolder(t *testing.T) {
	tests := []struct {
		name     string
		noPrompt bool
		accept   bool
	}{
		{name: "accept", accept: true},
		{name: "decline"},
		{name: "no prompt", noPrompt: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			generator := &actionInfrastructureGenerator{}
			action, dir := generationExtensionAction(t, generator)
			action.flags.force = false
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra", "data"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "infra", "data", "notes.txt"), []byte("x"), 0o600))

			active, cleaned := false, false
			action.extensionActivator = &actionGenerationActivator{
				ensure: func(context.Context, []string, string) (func(), error) {
					active = true
					return func() { active, cleaned = false, true }, nil
				},
			}

			confirmed := false
			console := mockinput.NewMockConsole()
			console.SetNoPromptMode(tt.noPrompt)
			if !tt.noPrompt {
				console.WhenConfirm(func(input.ConsoleOptions) bool {
					require.True(t, active, "extension must be activated before confirmation")
					confirmed = tt.accept
					return true
				}).Respond(tt.accept)
			}
			action.console = console

			generated := false
			generator.generate = func(
				_ context.Context, request *v1beta.InfraGenerateRequest,
			) (*v1beta.InfraGenerateResponse, error) {
				require.True(t, confirmed, "generator must not run without overwrite confirmation")
				require.True(t, request.Options.Force, "accepting the prompt must allow the extension to overwrite")
				generated = true
				return &v1beta.InfraGenerateResponse{Entry: &v1beta.ProvisioningOptions{
					Provider: "bicep", Path: request.OutputPath, Module: "main",
				}}, nil
			}
			_, err := action.Run(t.Context())
			switch {
			case tt.accept:
				require.NoError(t, err)
			case tt.noPrompt:
				suggestion, ok := errors.AsType[*internal.ErrorWithSuggestion](err)
				require.True(t, ok, "expected overwrite error, got %v", err)
				require.Contains(t, suggestion.Suggestion, "--force")
			default:
				require.ErrorIs(t, err, internal.ErrAbortedByUser)
			}
			require.Equal(t, tt.accept, generated)
			require.True(t, cleaned)
		})
	}
}

func TestInfraGenerateBuiltInDoesNotActivateExtensions(t *testing.T) {
	action, _ := generationExtensionAction(t, &actionInfrastructureGenerator{})
	action.projectConfig.Infra = provisioning.Options{Provider: provisioning.Bicep}
	action.extensionActivator = &actionGenerationActivator{
		ensure: func(context.Context, []string, string) (func(), error) {
			t.Error("built-in generation must not start extension listeners")
			return func() {}, nil
		},
	}
	_, err := action.Run(t.Context())
	require.ErrorContains(t, err, "does not contain any infrastructure to generate")
}

func TestInfraGenerateActivationFailureDoesNotWriteFiles(t *testing.T) {
	action, dir := generationExtensionAction(t, &actionInfrastructureGenerator{})
	failure := errors.New("generator listener failed")
	action.extensionActivator = &actionGenerationActivator{
		ensure: func(context.Context, []string, string) (func(), error) {
			return func() {}, failure
		},
	}
	_, err := action.Run(t.Context())
	require.ErrorIs(t, err, failure)
	require.NoDirExists(t, filepath.Join(dir, "infra"))
}

func TestInfraGenerateCleansUpListenerAfterExtensionFailure(t *testing.T) {
	failure := errors.New("extension write failed")
	generator := &actionInfrastructureGenerator{generate: func(
		context.Context, *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		return nil, failure
	}}
	action, dir := generationExtensionAction(t, generator)
	original, err := os.ReadFile(filepath.Join(dir, "azure.yaml"))
	require.NoError(t, err)
	cleaned := false
	action.extensionActivator = &actionGenerationActivator{
		ensure: func(context.Context, []string, string) (func(), error) {
			return func() { cleaned = true }, nil
		},
	}
	_, err = action.Run(t.Context())
	require.ErrorIs(t, err, failure)
	require.True(t, cleaned)
	actual, err := os.ReadFile(filepath.Join(dir, "azure.yaml"))
	require.NoError(t, err)
	require.Equal(t, original, actual)
}

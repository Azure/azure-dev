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
	dir := t.TempDir()
	source := "name: example\ninfra:\n  layers:\n    - name: data\n      provider: resources\n"
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

func TestInfraGenerateNonEmptyExtensionFolderPromptsAndForces(t *testing.T) {
	generator := &actionInfrastructureGenerator{}
	action, dir := generationExtensionAction(t, generator)
	action.flags.force = false
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra", "data"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "infra", "data", "notes.txt"), []byte("x"), 0o600))

	console := mockinput.NewMockConsole()
	console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(true)
	action.console = console

	generator.generate = func(
		_ context.Context, request *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		require.True(t, request.Options.Force, "accepting the prompt must allow the extension to overwrite")
		return &v1beta.InfraGenerateResponse{Entry: &v1beta.ProvisioningOptions{
			Provider: "bicep", Path: request.OutputPath, Module: "main",
		}}, nil
	}
	_, err := action.Run(t.Context())
	require.NoError(t, err)
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

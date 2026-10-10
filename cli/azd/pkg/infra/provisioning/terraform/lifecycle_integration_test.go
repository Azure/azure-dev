// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package terraform

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	terraformTools "github.com/azure/azure-dev/cli/azd/pkg/tools/terraform"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type terraformLifecycleCommands struct {
	applyLayers   []string
	destroyLayers []string
	destroyArgs   map[string][]string
	initArgs      map[string][][]string
}

func TestTerraformLifecycle_MapsLayerInputsOutputsAndTeardown(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)
	t.Setenv("AZURE_DEV_COLLECT_TELEMETRY", "no")
	t.Setenv("AZD_FORCE_TTY", "false")
	t.Setenv("NO_COLOR", "1")

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "azure.yaml"), []byte(`
name: lifecycle-test
infra:
  layers:
    - name: app
      provider: terraform
      path: infra/app
      module: main
      paramAliases:
        LAYER_VALUE: APP_INPUT
      outputAliases:
        OUTPUT: APP_OUTPUT
    - name: data
      provider: terraform
      path: infra/data
      module: main
      paramAliases:
        LAYER_VALUE: DATA_INPUT
      outputAliases:
        OUTPUT: DATA_OUTPUT
`), 0o600))

	for _, layer := range []string{"app", "data"} {
		infraDir := filepath.Join(projectDir, "infra", layer)
		require.NoError(t, os.MkdirAll(infraDir, 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(infraDir, "main.tf"),
			[]byte("terraform {}\n"),
			0o600,
		))
		require.NoError(t, os.WriteFile(
			filepath.Join(infraDir, "main.tfvars.json"),
			[]byte(`{"layerValue":"${LAYER_VALUE}"}`),
			0o600,
		))
	}

	projectConfig, err := project.Load(t.Context(), filepath.Join(projectDir, "azure.yaml"))
	require.NoError(t, err)
	layers := projectConfig.Infra.GetLayers()
	require.Len(t, layers, 2)

	backing := environment.NewWithValues("test-env", map[string]string{
		environment.SubscriptionIdEnvVarName: "test-subscription",
		environment.LocationEnvVarName:       "westus2",
		"OUTPUT":                             "shared-output",
		"APP_INPUT":                          "app",
		"DATA_INPUT":                         "data",
		"APP_OUTPUT":                         "stale-app-output",
		"DATA_OUTPUT":                        "stale-data-output",
	})
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, backing).Return(nil)
	commands := &terraformLifecycleCommands{
		destroyArgs: map[string][]string{},
		initArgs:    map[string][][]string{},
	}

	type layerLifecycle struct {
		options     provisioning.Options
		physicalKey string
		manager     *provisioning.Manager
	}

	layerLifecycles := make([]layerLifecycle, 0, len(layers))
	for _, layer := range layers {
		outputKey := strings.ToUpper(layer.Name) + "_OUTPUT"
		scoped := environment.NewMappedScopedEnvironment(
			backing,
			layer.ParamAliases,
			layer.OutputAliases,
		)

		manager := newTerraformLifecycleManager(t, projectDir, layer, scoped, envManager, commands)
		_, err := manager.Deploy(t.Context())
		require.NoError(t, err)

		parametersPath := filepath.Join(projectDir, ".azure", backing.Name(), layer.Path, "main.tfvars.json")
		parametersBytes, err := os.ReadFile(parametersPath)
		require.NoError(t, err)
		var parameters map[string]string
		require.NoError(t, json.Unmarshal(parametersBytes, &parameters))
		require.Equal(t, map[string]string{"layerValue": layer.Name}, parameters)

		layerLifecycles = append(layerLifecycles, layerLifecycle{
			options:     layer,
			physicalKey: outputKey,
			manager:     manager,
		})
	}

	require.Equal(t, []string{"app", "data"}, commands.applyLayers)
	require.Equal(t, "shared-output", backing.Getenv("OUTPUT"))
	require.Equal(t, "app-output", backing.Getenv("APP_OUTPUT"))
	require.Equal(t, "data-output", backing.Getenv("DATA_OUTPUT"))

	slices.Reverse(layerLifecycles)
	for index, layer := range layerLifecycles {
		layer.options.Mode = provisioning.ModeDestroy
		require.NoError(t, layer.manager.Initialize(t.Context(), projectDir, layer.options))

		destroyResult, err := layer.manager.Destroy(t.Context(), provisioning.NewDestroyOptions(true, false))
		require.NoError(t, err)
		require.Equal(t, []string{"OUTPUT"}, destroyResult.InvalidatedEnvKeys)
		require.Empty(t, backing.Getenv(layer.physicalKey))
		require.Equal(t, "shared-output", backing.Getenv("OUTPUT"))

		if index == 0 {
			require.Equal(t, "app-output", backing.Getenv("APP_OUTPUT"))
		}
	}

	require.Equal(t, []string{"data", "app"}, commands.destroyLayers)
	for _, layer := range []string{"app", "data"} {
		require.Len(t, commands.initArgs[layer], 2)
		require.Contains(t, commands.initArgs[layer][0], "-upgrade")
		require.Contains(t, commands.initArgs[layer][1], "-input=false")
		require.NotContains(t, commands.initArgs[layer][1], "-upgrade")
		require.Contains(t, commands.destroyArgs[layer], "-auto-approve")
	}
	envManager.AssertExpectations(t)
}

func newTerraformLifecycleManager(
	t *testing.T,
	projectDir string,
	options provisioning.Options,
	env environment.ScopedEnvironment,
	envManager environment.Manager,
	commands *terraformLifecycleCommands,
) *provisioning.Manager {
	t.Helper()

	// Every Terraform command must match one of the responses below. The mock runner panics instead
	// of invoking a real binary, which keeps this test independent of Terraform and external services.
	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Console.SetNoPromptMode(true)
	mockContext.CommandRunner.MockToolInPath("terraform", nil)
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "terraform" && strings.Contains(command, "version")
	}).Respond(exec.RunResult{Stdout: `{"terraform_version":"1.5.0"}`})
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "terraform" && strings.Contains(command, "init")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		layer := terraformLayerFromArgs(args)
		commands.initArgs[layer] = append(commands.initArgs[layer], slices.Clone(args.Args))
		return exec.RunResult{Stdout: "Terraform has been successfully initialized!"}, nil
	})
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "terraform" && strings.Contains(command, "validate")
	}).Respond(exec.RunResult{Stdout: "Success! The configuration is valid."})
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "terraform" && strings.Contains(command, "plan")
	}).Respond(exec.RunResult{Stdout: "Plan: 1 to add, 0 to change, 0 to destroy."})
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "terraform" && strings.Contains(command, "apply")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		commands.applyLayers = append(commands.applyLayers, terraformLayerFromArgs(args))
		return exec.RunResult{Stdout: "Apply complete! Resources: 1 added."}, nil
	})
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "terraform" && strings.Contains(command, "output")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		layer := terraformLayerFromArgs(args)
		return exec.RunResult{Stdout: fmt.Sprintf(
			`{"OUTPUT":{"sensitive":false,"type":"string","value":%q}}`,
			layer+"-output",
		)}, nil
	})
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "terraform" && strings.Contains(command, "destroy")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		layer := terraformLayerFromArgs(args)
		commands.destroyLayers = append(commands.destroyLayers, layer)
		commands.destroyArgs[layer] = slices.Clone(args.Args)
		return exec.RunResult{Stdout: "Destroy complete! Resources: 1 destroyed."}, nil
	})

	provider := NewTerraformProvider(
		terraformTools.NewCli(mockContext.CommandRunner),
		envManager,
		env,
		mockContext.Console,
		&mockCurrentPrincipal{},
		nil,
	)
	ioc.RegisterNamedInstance[provisioning.Provider](
		mockContext.Container,
		string(provisioning.Terraform),
		provider,
	)

	manager := provisioning.NewManager(
		mockContext.Container,
		func() (provisioning.ProviderKind, error) {
			return provisioning.Terraform, nil
		},
		envManager,
		env,
		mockContext.Console,
		alpha.NewFeaturesManagerWithConfig(mockContext.Config),
		nil,
		cloud.AzurePublic(),
	)
	require.NoError(t, manager.Initialize(t.Context(), projectDir, options))
	return manager
}

func terraformLayerFromArgs(args exec.RunArgs) string {
	modulePath := strings.TrimPrefix(args.Args[0], "-chdir=")
	return filepath.Base(modulePath)
}

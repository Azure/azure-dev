// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulator"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulators"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type fakeLauncher struct {
	dockerHost          emulator.DockerHost
	dockerErr           error
	requirementsChecked []string
	requirementFailure  string
	definition          emulator.Definition
	request             emulator.Request
	calls               int
	logMessage          string
}

func (f *fakeLauncher) CheckDocker(context.Context) (emulator.DockerHost, error) {
	return f.dockerHost, f.dockerErr
}

func (f *fakeLauncher) CheckRequirements(_ context.Context, definition emulator.Definition) error {
	f.requirementsChecked = append(f.requirementsChecked, definition.Name)
	if definition.Name == f.requirementFailure {
		return fmt.Errorf("%s requires Docker Compose", definition.DisplayName)
	}
	return nil
}

func (f *fakeLauncher) Run(
	_ context.Context, definition emulator.Definition, request emulator.Request,
) (emulator.Result, error) {
	f.calls++
	f.definition = definition
	f.request = request
	if f.logMessage != "" {
		log.Print(f.logMessage)
	}
	if request.Stop {
		return emulator.Result{Emulator: definition.Name, Status: "stopped",
			RemovedConnectionNames: []string{"CONNECTION_STRING"}}, nil
	}
	return emulator.Result{
		Emulator: definition.Name, Status: "running", ProjectName: "test-project",
		Connections: []emulator.Connection{{Name: "CONNECTION_STRING", Value: "Endpoint=localhost;"}},
	}, nil
}

func testRoot(t *testing.T, service *fakeLauncher) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	logger, writer, flags := slog.Default(), log.Writer(), log.Flags()
	t.Cleanup(func() {
		slog.SetDefault(logger)
		log.SetOutput(writer)
		log.SetFlags(flags)
	})
	root := newRootCommand("0.1.0", func() (launcher, error) { return service, nil }, nil)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(stderr)
	return root, stdout, stderr
}

func TestEmulatorCommandPrintsJSONWithoutProgressContamination(t *testing.T) {
	service := &fakeLauncher{}
	root, stdout, stderr := testRoot(t, service)
	root.SetArgs([]string{"servicebus", "--accept-license", "--port", "15672", "--output", "json", "--no-prompt"})
	require.NoError(t, root.ExecuteContext(t.Context()))
	require.Equal(t, "servicebus", service.definition.Name)
	require.Equal(t, 15672, service.request.Options.Ports["port"])
	require.True(t, service.request.AcceptLicense)
	require.True(t, service.request.Changed["port"])

	var result emulator.Result
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
	require.Equal(t, "Endpoint=localhost;", result.Connections[0].Value)
	require.Contains(t, stderr.String(), "Starting Azure Service Bus")
}

func TestEmulatorCommandsExposeAllLaunchersAndMetadata(t *testing.T) {
	root, stdout, _ := testRoot(t, &fakeLauncher{})
	require.NoError(t, azdext.ValidateNoReservedFlagConflicts(root))
	for _, definition := range emulators.All() {
		command, _, err := root.Find([]string{definition.Name})
		require.NoError(t, err)
		require.Equal(t, definition.Name, command.Name())
		require.NotNil(t, command.RunE)
	}
	root.SetArgs([]string{"metadata"})
	require.NoError(t, root.ExecuteContext(t.Context()))
	require.True(t, json.Valid(stdout.Bytes()))
	for _, definition := range emulators.All() {
		require.Contains(t, stdout.String(), `"`+definition.Name+`"`)
	}
}

func TestEmulatorCommandRejectsDroppedFlagsAndInvalidInput(t *testing.T) {
	for _, args := range [][]string{
		{"storage", "--stop", "--port", "10000"},
		{"signalr", "--stop", "--upstream-url", "http://localhost:7071"},
		{"servicebus", "--stop", "--accept-license=false"},
		{"storage", "--port", "0"},
		{"storage", "--port", "10001"},
		{"storage", "--timeout", "0s"},
		{"storage", "unexpected"},
		{"storage", "--output", "xml"},
		{"storage", "--environment", "dev"},
	} {
		t.Run(args[0]+" "+args[1], func(t *testing.T) {
			service := &fakeLauncher{}
			root, stdout, _ := testRoot(t, service)
			root.SetArgs(args)
			require.Error(t, root.ExecuteContext(t.Context()))
			require.Zero(t, service.calls)
			require.Empty(t, stdout.String())
		})
	}
}

func TestEmulatorCommandSuppressesAllOutputWhenRequested(t *testing.T) {
	root, stdout, stderr := testRoot(t, &fakeLauncher{logMessage: "runner diagnostic"})
	root.SetArgs([]string{"storage", "--output", "none", "--debug"})
	require.NoError(t, root.ExecuteContext(t.Context()))
	require.Empty(t, stdout.String())
	require.Empty(t, stderr.String())
}

func TestEmulatorCommandPrintsConnectionInfoToInjectedWriter(t *testing.T) {
	root, stdout, _ := testRoot(t, &fakeLauncher{})
	root.SetArgs([]string{"storage"})
	require.NoError(t, root.ExecuteContext(t.Context()))
	require.Contains(t, stdout.String(), "CONNECTION_STRING: Endpoint=localhost;")
	require.Contains(t, stdout.String(), "azd emulators storage --stop")
}

func TestEmulatorCommandKeepsDebugLogsOnInjectedErrorWriter(t *testing.T) {
	root, stdout, stderr := testRoot(t, &fakeLauncher{logMessage: "runner diagnostic"})
	root.SetArgs([]string{"storage", "--output", "json", "--debug"})
	require.NoError(t, root.ExecuteContext(t.Context()))
	require.True(t, json.Valid(stdout.Bytes()))
	require.Contains(t, stderr.String(), "runner diagnostic")
}

func TestEmulatorAddListenerIgnoresConnectionResetOnlyAfterSuccess(t *testing.T) {
	err := fmt.Errorf("receiver error: stream receive failed: %w",
		status.Error(codes.Unavailable, "error reading from server: connection reset by peer"))

	require.NoError(t, addListenerError(true, err))
	require.ErrorIs(t, addListenerError(false, err), err)
}

func TestEmulatorAddListenerPreservesOtherUnavailableErrors(t *testing.T) {
	err := fmt.Errorf("receiver error: stream receive failed: %w",
		status.Error(codes.Unavailable, "connection refused"))

	require.ErrorIs(t, addListenerError(true, err), err)
}

func TestEmulatorAddOffersTheEntireCatalog(t *testing.T) {
	root, _, _ := testRoot(t, &fakeLauncher{})
	listen, _, err := root.Find([]string{"listen"})
	require.NoError(t, err)
	require.Equal(t, "listen", listen.Name())
	require.True(t, listen.Hidden)

	definitions := emulators.All()
	service := &fakeLauncher{dockerHost: emulator.DockerHost{Architecture: "amd64"}}
	items := emulatorAddItems(t.Context(), definitions, func() (launcher, error) { return service, nil })
	require.Len(t, items, len(definitions))
	seen := map[string]bool{}
	for i, item := range items {
		require.Equal(t, definitions[i].Name, item.GetId())
		require.False(t, seen[item.GetId()])
		seen[item.GetId()] = true
		require.Equal(t, definitions[i].DisplayName, item.GetDisplayName())
		require.Empty(t, item.GetDisabledReason())
	}
}

func TestEmulatorAddDisablesAMD64OnlyItemsOnARM64(t *testing.T) {
	service := &fakeLauncher{dockerHost: emulator.DockerHost{Architecture: "aarch64"}}
	items := emulatorAddItems(t.Context(), emulators.All(), func() (launcher, error) { return service, nil })
	disabled := map[string]string{}
	for _, item := range items {
		if item.GetDisabledReason() != "" {
			disabled[item.GetId()] = item.GetDisabledReason()
		}
	}
	require.Len(t, disabled, 2)
	require.Contains(t, disabled["servicebus"], "AMD64")
	require.Contains(t, disabled["kusto"], "AMD64")
}

func TestEmulatorAddDisablesItemsWithoutDockerPrerequisites(t *testing.T) {
	for _, reason := range []string{"Docker is required to run emulators.", "`docker compose` is unavailable."} {
		t.Run(reason, func(t *testing.T) {
			service := &fakeLauncher{dockerErr: fmt.Errorf("%s", reason)}
			definitions := emulators.All()
			items := emulatorAddItems(t.Context(), definitions, func() (launcher, error) { return service, nil })
			require.Len(t, items, len(definitions))
			for _, item := range items {
				require.Equal(t, reason, item.GetDisabledReason())
			}
		})
	}
}

func TestEmulatorAddOnlyWritesLayerConfig(t *testing.T) {
	cloud := map[string]any{"name": "cloud", "provider": "bicep", "path": "infra"}
	project := &emulatorAddProject{infra: map[string]any{"layers": []any{cloud}}}
	require.NoError(t, addEmulator(t.Context(), project, nil, emulators.All(), "storage"))
	require.NoError(t, addEmulator(t.Context(), project, nil, emulators.All(), "servicebus"))
	require.Equal(t, []any{cloud, map[string]any{
		"name": "emulators", "provider": "emulators", "config": map[string]any{
			"storage": map[string]any{}, "servicebus": map[string]any{},
		},
	}}, project.infra["layers"])
	require.Equal(t, 2, project.writes)
	require.Error(t, addEmulator(t.Context(), project, nil, emulators.All(), "storage"))
	require.Equal(t, 2, project.writes)
}

func TestEmulatorAddPreservesProjectLayers(t *testing.T) {
	project := &emulatorAddProject{layers: []any{
		map[string]any{"name": "app", "infra": []any{map[string]any{"name": "cloud", "provider": "bicep"}}},
	}}
	require.NoError(t, addEmulator(t.Context(), project, nil, emulators.All(), "storage"))
	require.NoError(t, addEmulator(t.Context(), project, nil, emulators.All(), "appconfig"))
	require.Equal(t, []any{
		map[string]any{"name": "app", "infra": []any{map[string]any{"name": "cloud", "provider": "bicep"}}},
		map[string]any{"name": "emulators", "infra": []any{map[string]any{
			"name": "emulators", "provider": "emulators", "config": map[string]any{
				"storage": map[string]any{}, "appconfig": map[string]any{},
			},
		}}},
	}, project.layers)
	require.Nil(t, project.infra)
}

func TestEmulatorLayerLaunchesOnlyDuringDeploy(t *testing.T) {
	projectDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(projectDir, "site output"), 0o700))
	service := &fakeLauncher{}
	project := &emulatorAddProject{}
	require.NoError(t, addEmulator(t.Context(), project, &emulatorAddPrompt{directory: "site output"},
		emulators.All(), "staticwebapps"))
	layers, ok := project.infra["layers"].([]any)
	require.True(t, ok)
	require.Len(t, layers, 1)
	layer, ok := layers[0].(map[string]any)
	require.True(t, ok)
	options, ok := layer["config"].(map[string]any)
	require.True(t, ok)
	config, err := structpb.NewStruct(options)
	require.NoError(t, err)
	provider := &emulatorProvider{
		definitions:        emulators.All(),
		currentEnvironment: func(context.Context) (string, error) { return "dev", nil },
		resolve: func(dir, env string) (launcher, error) {
			require.Equal(t, projectDir, dir)
			require.Equal(t, "dev", env)
			return service, nil
		},
	}
	require.NoError(t, provider.Initialize(t.Context(), projectDir, &azdext.ProvisioningOptions{Config: config}))
	_, err = provider.Preview(t.Context(), nil)
	require.NoError(t, err)
	require.Zero(t, service.calls)
	deployed, err := provider.Deploy(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, 1, service.calls)
	require.Equal(t, "staticwebapps", service.definition.Name)
	require.Equal(t, filepath.Join(projectDir, "site output"), service.request.Options.Directory)
	require.True(t, service.request.Changed["directory"])
	outputs := deployed.GetDeployment().GetOutputs()
	require.Len(t, outputs, 1)
	const key = "AZD_EMULATOR_STATICWEBAPPS_CONNECTION_STRING"
	require.Equal(t, "string", outputs[key].GetType())
	require.Equal(t, "Endpoint=localhost;", outputs[key].GetValue())
	destroyed, err := provider.Destroy(t.Context(), nil, nil)
	require.NoError(t, err)
	require.True(t, service.request.Stop)
	require.Equal(t, []string{key}, destroyed.GetInvalidatedEnvKeys())
}

func TestEmulatorDeployHonorsExplicitLicenseDecline(t *testing.T) {
	config, err := structpb.NewStruct(map[string]any{
		"appconfig": map[string]any{}, "servicebus": map[string]any{"acceptLicense": false},
	})
	require.NoError(t, err)
	provider := &emulatorProvider{definitions: emulators.All()}
	require.NoError(t, provider.Initialize(t.Context(), t.TempDir(), &azdext.ProvisioningOptions{Config: config}))
	_, err = provider.Deploy(t.Context(), func(string) { t.Error("startup progress reported before license validation") })
	var local *azdext.LocalError
	require.ErrorAs(t, err, &local)
	require.Contains(t, local.Suggestion, "acceptLicense: true")
}

func TestEmulatorDeployAcceptsLicenseByDefault(t *testing.T) {
	config, err := structpb.NewStruct(map[string]any{"servicebus": map[string]any{}})
	require.NoError(t, err)
	service := &fakeLauncher{}
	provider := &emulatorProvider{
		definitions:        emulators.All(),
		currentEnvironment: func(context.Context) (string, error) { return "dev", nil },
		resolve:            func(string, string) (launcher, error) { return service, nil },
	}
	require.NoError(t, provider.Initialize(t.Context(), t.TempDir(), &azdext.ProvisioningOptions{Config: config}))
	_, err = provider.Deploy(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, 1, service.calls)
	require.True(t, service.request.AcceptLicense)
}

func TestEmulatorDeployChecksEverySelectedEmulatorBeforeStarting(t *testing.T) {
	config, err := structpb.NewStruct(map[string]any{"appconfig": map[string]any{}, "servicebus": map[string]any{}})
	require.NoError(t, err)
	service := &fakeLauncher{requirementFailure: "servicebus"}
	provider := &emulatorProvider{
		definitions:        emulators.All(),
		currentEnvironment: func(context.Context) (string, error) { return "dev", nil },
		resolve:            func(string, string) (launcher, error) { return service, nil },
	}
	require.NoError(t, provider.Initialize(t.Context(), t.TempDir(), &azdext.ProvisioningOptions{Config: config}))
	_, err = provider.Deploy(t.Context(), nil)
	require.ErrorContains(t, err, "Azure Service Bus requires Docker Compose")
	require.Equal(t, []string{"appconfig", "servicebus"}, service.requirementsChecked)
	require.Zero(t, service.calls)
}

type emulatorAddPrompt struct {
	azdext.PromptServiceClient
	directory string
}

func (p *emulatorAddPrompt) Prompt(
	_ context.Context, _ *azdext.PromptRequest, _ ...grpc.CallOption,
) (*azdext.PromptResponse, error) {
	return &azdext.PromptResponse{Value: p.directory}, nil
}

type emulatorAddProject struct {
	azdext.ProjectServiceClient
	infra  map[string]any
	layers []any
	writes int
}

func (p *emulatorAddProject) GetConfigValue(
	_ context.Context, req *azdext.GetProjectConfigValueRequest, _ ...grpc.CallOption,
) (*azdext.GetProjectConfigValueResponse, error) {
	var raw any
	switch {
	case req.Path == "layers" && p.layers != nil:
		raw = p.layers
	case req.Path == "infra" && p.infra != nil:
		raw = p.infra
	default:
		return &azdext.GetProjectConfigValueResponse{}, nil
	}
	value, err := structpb.NewValue(raw)
	return &azdext.GetProjectConfigValueResponse{Found: true, Value: value}, err
}

func (p *emulatorAddProject) SetConfigValue(
	_ context.Context, req *azdext.SetProjectConfigValueRequest, _ ...grpc.CallOption,
) (*azdext.EmptyResponse, error) {
	p.writes++
	if req.Path == "layers" {
		p.layers = req.Value.GetListValue().AsSlice()
		return &azdext.EmptyResponse{}, nil
	}
	if p.infra == nil {
		p.infra = map[string]any{}
	}
	p.infra["layers"] = req.Value.AsInterface()
	return &azdext.EmptyResponse{}, nil
}

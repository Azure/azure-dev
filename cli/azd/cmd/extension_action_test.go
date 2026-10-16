// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
)

func newExtensionActionTestManager(
	t *testing.T,
	mockCtx *mocks.MockContext,
	extension *extensions.Extension,
) *extensions.Manager {
	t.Helper()

	userConfigManager := config.NewUserConfigManager(mockCtx.ConfigManager)
	sourceManager := extensions.NewSourceManager(
		mockCtx.Container,
		userConfigManager,
		mockCtx.HttpClient,
	)
	lazyRunner := lazy.NewLazy(func() (*extensions.Runner, error) {
		return extensions.NewRunner(mockCtx.CommandRunner), nil
	})
	manager, err := extensions.NewManager(
		userConfigManager,
		sourceManager,
		lazyRunner,
		mockCtx.HttpClient,
	)
	require.NoError(t, err)

	cfg, err := userConfigManager.Load()
	require.NoError(t, err)
	require.NoError(t, cfg.Set(
		"extension.installed",
		map[string]*extensions.Extension{extension.Id: extension},
	))

	return manager
}

func newExtensionActionTestServer() *grpcserver.Server {
	return grpcserver.NewServer(
		&azdext.UnimplementedProjectServiceServer{},
		&azdext.UnimplementedEnvironmentServiceServer{},
		&azdext.UnimplementedPromptServiceServer{},
		&azdext.UnimplementedUserConfigServiceServer{},
		&azdext.UnimplementedDeploymentServiceServer{},
		&azdext.UnimplementedEventServiceServer{},
		&v1beta.UnimplementedComposeServiceServer{},
		&azdext.UnimplementedWorkflowServiceServer{},
		&azdext.UnimplementedExtensionServiceServer{},
		&azdext.UnimplementedServiceTargetServiceServer{},
		&azdext.UnimplementedFrameworkServiceServer{},
		&azdext.UnimplementedContainerServiceServer{},
		&azdext.UnimplementedAccountServiceServer{},
		&azdext.UnimplementedAiModelServiceServer{},
		&v1beta.UnimplementedCopilotServiceServer{},
		&azdext.UnimplementedProvisioningServiceServer{},
		&azdext.UnimplementedValidationServiceServer{},
		&azdext.UnimplementedTelemetryServiceServer{},
		&v1beta.UnimplementedCommandResultServiceServer{},
	)
}

func TestExtensionAction_Run_PropagatesTraceContext(t *testing.T) {
	originalArgs := os.Args
	os.Args = []string{"azd", "--output=json"}
	t.Cleanup(func() {
		os.Args = originalArgs
	})

	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)
	extensionPath := filepath.Join("extensions", "test-ext", "bin", "test-ext")
	fullPath := filepath.Join(configDir, extensionPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
	require.NoError(t, os.WriteFile(fullPath, []byte("test"), 0o600))

	mockCtx := mocks.NewMockContext(t.Context())
	var captured exec.RunArgs
	mockCtx.CommandRunner.When(func(args exec.RunArgs, _ string) bool {
		captured = args
		return true
	}).Respond(exec.NewRunResult(0, "", ""))

	extension := &extensions.Extension{
		Id:      "test-ext",
		Path:    extensionPath,
		Version: "1.0.0",
	}
	traceparent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	ctx := propagation.TraceContext{}.Extract(t.Context(), propagation.MapCarrier{
		"traceparent": traceparent,
		"tracestate":  "vendor=value",
	})
	action := &extensionAction{
		console:          mockCtx.Console,
		extensionRunner:  extensions.NewRunner(mockCtx.CommandRunner),
		lazyEnv:          lazy.From[*environment.Environment](nil),
		extensionManager: newExtensionActionTestManager(t, mockCtx, extension),
		azdServer:        newExtensionActionTestServer(),
		globalOptions: &internal.GlobalCommandOptions{
			EnableDebugLogging: true,
			NoPrompt:           true,
			Cwd:                "work",
			EnvironmentName:    "test",
		},
		cmd: &cobra.Command{
			Annotations: map[string]string{"extension.id": extension.Id},
		},
		args: []string{"telemetry"},
	}

	result, err := action.Run(ctx)

	require.NoError(t, err)
	require.Nil(t, result)
	require.Equal(t, fullPath, captured.Cmd)
	require.Equal(t, []string{"telemetry"}, captured.Args)
	require.Contains(t, captured.Env, "TRACEPARENT="+traceparent)
	require.Contains(t, captured.Env, "TRACESTATE=vendor=value")
	require.Contains(t, captured.Env, "AZD_DEBUG=true")
	require.Contains(t, captured.Env, "AZD_NO_PROMPT=true")
	require.Contains(t, captured.Env, "AZD_CWD=work")
	require.Contains(t, captured.Env, "AZD_ENVIRONMENT=test")
}

func TestExtensionAction_RunCommandLaunch(t *testing.T) {
	originalArgs := os.Args
	os.Args = []string{"azd", "--output=json"}
	t.Cleanup(func() {
		os.Args = originalArgs
	})

	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)
	t.Setenv("EXTENSION_TEST_PARENT", "parent-value")
	t.Setenv("EXTENSION_TEST_AZD_ENV", "")
	extensionPath := filepath.Join("extensions", "test-ext", "bin", "test-ext")
	fullPath := filepath.Join(configDir, extensionPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
	require.NoError(t, os.WriteFile(fullPath, []byte("test"), 0o600))

	metadataDir := filepath.Join(configDir, "extensions", "test-ext")
	require.NoError(t, os.MkdirAll(metadataDir, 0o755))
	metadata, err := json.Marshal(&extensions.ExtensionCommandMetadata{
		ID: "test-ext",
		Commands: []extensions.Command{
			{
				Name:   []string{"local"},
				Launch: &extensions.CommandLaunch{NoAzdEnvironment: true},
				Subcommands: []extensions.Command{{
					Name: []string{"local", "child"},
				}},
			},
			{
				Name:    []string{"connection"},
				Aliases: []string{"conn"},
				Launch:  &extensions.CommandLaunch{NoAzdEnvironment: true},
				Subcommands: []extensions.Command{{
					Name:    []string{"connection", "add"},
					Aliases: []string{"create"},
					Launch:  &extensions.CommandLaunch{NoAzdEnvironment: true},
				}, {
					Name: []string{"connection", "list"},
				}},
			},
			{Name: []string{"default"}},
			{Name: []string{"explicit-default"}, Launch: &extensions.CommandLaunch{NoAzdEnvironment: false}},
		},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(metadataDir, "metadata.json"), metadata, 0o600))

	mockCtx := mocks.NewMockContext(t.Context())
	var captured exec.RunArgs
	mockCtx.CommandRunner.When(func(args exec.RunArgs, _ string) bool {
		captured = args
		return true
	}).Respond(exec.NewRunResult(0, "", ""))

	extension := &extensions.Extension{
		Id:           "test-ext",
		Path:         extensionPath,
		Version:      "1.0.0",
		Capabilities: []extensions.CapabilityType{extensions.MetadataCapability},
	}
	action := &extensionAction{
		console:          mockCtx.Console,
		extensionRunner:  extensions.NewRunner(mockCtx.CommandRunner),
		extensionManager: newExtensionActionTestManager(t, mockCtx, extension),
		azdServer:        newExtensionActionTestServer(),
		globalOptions: &internal.GlobalCommandOptions{
			EnvironmentName: "test",
		},
		cmd: &cobra.Command{
			Annotations: map[string]string{"extension.id": extension.Id},
		},
	}

	for _, tt := range []struct {
		args             []string
		noAzdEnvironment bool
	}{
		{[]string{"local"}, true},
		{[]string{"connection"}, true},
		{[]string{"conn"}, true},
		{[]string{"connection", "add"}, true},
		{[]string{"conn", "add"}, true},
		{[]string{"connection", "create"}, true},
		{[]string{"conn", "create"}, true},
		{[]string{"default"}, false},
		{[]string{"explicit-default"}, false},
		{[]string{"local", "child"}, false},
		{[]string{"connection", "list"}, false},
		{[]string{"conn", "list"}, false},
		{[]string{"unknown"}, false},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			for _, prefix := range [][]string{
				{"--no-prompt", "-o", "json"},
				{"--trace-log-file", "trace.json", "--no-prompt"},
				{"--trace-log-url", "http://localhost:4318", "-ojson"},
				{"--debug", "--cwd", "project", "--environment", "test"},
				{"--help"},
				{"--docs"},
				{"--non-interactive"},
			} {
				t.Run(prefix[0], func(t *testing.T) {
					environmentLoads := 0
					action.lazyEnv = lazy.NewLazy(func() (*environment.Environment, error) {
						environmentLoads++
						return environment.NewWithValues("test", map[string]string{
							"EXTENSION_TEST_AZD_ENV": "azd-value",
						}), nil
					})
					action.args = append(slices.Clone(prefix), tt.args...)
					action.args = append(action.args, "--file", "connection.yaml")
					action.azdServer = newExtensionActionTestServer()
					result, err := action.Run(t.Context())

					require.NoError(t, err)
					require.Nil(t, result)
					if tt.noAzdEnvironment {
						require.Zero(t, environmentLoads)
						require.NotContains(t, captured.Env, "EXTENSION_TEST_AZD_ENV=azd-value")
					} else {
						require.Equal(t, 1, environmentLoads)
						require.Contains(t, captured.Env, "EXTENSION_TEST_AZD_ENV=azd-value")
					}
					require.Contains(t, captured.Env, "EXTENSION_TEST_PARENT=parent-value")
					require.Contains(t, captured.Env, "AZD_ENVIRONMENT=test")
					for _, key := range []string{"AZD_SERVER", "AZD_ACCESS_TOKEN"} {
						require.True(t, slices.ContainsFunc(captured.Env, func(value string) bool {
							return strings.HasPrefix(value, key+"=") && len(value) > len(key)+1
						}), "%s must be passed to the extension", key)
					}
				})
			}
		})
	}
}

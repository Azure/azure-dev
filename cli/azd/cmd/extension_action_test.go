// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
)

type cancellationBlindCommandRunner struct {
	started chan struct{}
}

func (r *cancellationBlindCommandRunner) Run(ctx context.Context, args exec.RunArgs) (exec.RunResult, error) {
	close(r.started)
	<-ctx.Done()
	return exec.NewRunResult(0, "", ""), nil
}

func (r *cancellationBlindCommandRunner) RunList(
	context.Context,
	[]string,
	exec.RunArgs,
) (exec.RunResult, error) {
	panic("unexpected RunList call")
}

func (r *cancellationBlindCommandRunner) ToolInPath(string) error {
	return nil
}

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

func TestExtensionAction_Run_InterruptUnwindsAsCancellation(t *testing.T) {
	originalGracePeriod := extensionCancellationGracePeriod
	extensionCancellationGracePeriod = 20 * time.Millisecond
	t.Cleanup(func() {
		extensionCancellationGracePeriod = originalGracePeriod
	})

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
	commandRunner := &cancellationBlindCommandRunner{started: make(chan struct{})}
	extension := &extensions.Extension{
		Id:      "test-ext",
		Path:    extensionPath,
		Version: "1.0.0",
	}
	action := &extensionAction{
		console:          mockCtx.Console,
		extensionRunner:  extensions.NewRunner(commandRunner),
		lazyEnv:          lazy.From[*environment.Environment](nil),
		extensionManager: newExtensionActionTestManager(t, mockCtx, extension),
		azdServer:        newExtensionActionTestServer(),
		globalOptions:    &internal.GlobalCommandOptions{},
		cmd: &cobra.Command{
			Annotations: map[string]string{"extension.id": extension.Id},
		},
	}

	initialHandlers := len(input.SnapshotInterruptStack())
	runCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := action.Run(runCtx)
		errCh <- err
	}()

	select {
	case <-commandRunner.started:
	case <-runCtx.Done():
		t.Fatal("extension invocation did not start")
	}

	handlers := input.SnapshotInterruptStack()
	require.Len(t, handlers, initialHandlers+1)
	handler := handlers[len(handlers)-1]
	require.True(t, handler())

	select {
	case err := <-errCh:
		require.Error(t, err)
		var runErr *extensions.ExtensionRunError
		require.ErrorAs(t, err, &runErr)
		require.ErrorIs(t, runErr, context.Canceled)
	case <-runCtx.Done():
		t.Fatal("extension invocation did not stop after interrupt")
	}

	require.Len(t, input.SnapshotInterruptStack(), initialHandlers)
}

func TestExtensionAction_Run_InterruptExitKeepsHandlerUntilSignalConsumed(t *testing.T) {
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
	mockCtx.CommandRunner.When(func(args exec.RunArgs, _ string) bool {
		return true
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(130, "", ""), &exec.ExitError{
			Cmd:      args.Cmd,
			ExitCode: 130,
		}
	})

	extension := &extensions.Extension{
		Id:      "test-ext",
		Path:    extensionPath,
		Version: "1.0.0",
	}
	action := &extensionAction{
		console:          mockCtx.Console,
		extensionRunner:  extensions.NewRunner(mockCtx.CommandRunner),
		lazyEnv:          lazy.From[*environment.Environment](nil),
		extensionManager: newExtensionActionTestManager(t, mockCtx, extension),
		azdServer:        newExtensionActionTestServer(),
		globalOptions:    &internal.GlobalCommandOptions{},
		cmd: &cobra.Command{
			Annotations: map[string]string{"extension.id": extension.Id},
		},
	}

	initialHandlers := len(input.SnapshotInterruptStack())
	_, err := action.Run(t.Context())
	require.ErrorIs(t, err, context.Canceled)

	handlers := input.SnapshotInterruptStack()
	require.Len(t, handlers, initialHandlers+1)
	require.True(t, handlers[len(handlers)-1]())
	require.Len(t, input.SnapshotInterruptStack(), initialHandlers)
}

func TestExtensionInterruptController_FirstInterruptMarksPosixRunCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	controller := newExtensionInterruptController(cancel, time.Hour)
	require.True(t, controller.handle())
	require.NoError(t, ctx.Err())
	require.ErrorIs(t, controller.finish(nil, &extensions.Extension{
		Id:      "test.ext",
		Version: "1.0.0",
	}), context.Canceled)
}

func TestExtensionInterruptController_SecondInterruptCancelsPosixInvocation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	controller := newExtensionInterruptController(cancel, time.Hour)
	require.True(t, controller.handle())
	require.True(t, controller.handle())
	require.ErrorIs(t, ctx.Err(), context.Canceled)

	err := controller.finish(nil, &extensions.Extension{
		Id:      "test.ext",
		Version: "1.0.0",
	})
	var runErr *extensions.ExtensionRunError
	require.ErrorAs(t, err, &runErr)
	require.Equal(t, "test.ext", runErr.ExtensionId)
	require.Equal(t, "1.0.0", runErr.ExtensionVersion)
	require.ErrorIs(t, err, context.Canceled)
}

func TestExtensionInterruptController_FirstInterruptCancelsWindowsInvocation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	controller := newExtensionInterruptController(cancel, 0)
	require.True(t, controller.handle())
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.False(t, controller.handle())
	require.ErrorIs(t, controller.finish(nil, &extensions.Extension{
		Id:      "test.ext",
		Version: "1.0.0",
	}), context.Canceled)
}

func TestExtensionInterruptController_FirstInterruptAutomaticallyCancelsAfterGrace(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	controller := newExtensionInterruptController(cancel, 10*time.Millisecond)
	require.True(t, controller.handle())
	require.Eventually(t, func() bool {
		return errors.Is(ctx.Err(), context.Canceled)
	}, time.Second, time.Millisecond)

	require.ErrorIs(t, controller.finish(nil, &extensions.Extension{
		Id:      "test.ext",
		Version: "1.0.0",
	}), context.Canceled)
}

func TestExtensionInterruptController_InterruptedFailureRetainsBothCauses(t *testing.T) {
	_, cancel := context.WithCancel(t.Context())
	defer cancel()

	controller := newExtensionInterruptController(cancel, time.Hour)
	require.True(t, controller.handle())

	processErr := errors.New("exit code 130")
	err := controller.finish(&extensions.ExtensionRunError{
		ExtensionId:      "test.ext",
		ExtensionVersion: "1.0.0",
		Err:              processErr,
	}, &extensions.Extension{
		Id:      "test.ext",
		Version: "1.0.0",
	})

	require.ErrorIs(t, err, processErr)
	require.ErrorIs(t, err, context.Canceled)
}

func TestExtensionInterruptController_InterruptExitWaitsForHostSignal(t *testing.T) {
	initialHandlers := len(input.SnapshotInterruptStack())
	_, controller, cleanup := installExtensionInterruptHandler(t.Context())
	defer cleanup()

	err := controller.finish(&extensions.ExtensionRunError{
		ExtensionId:      "test.ext",
		ExtensionVersion: "1.0.0",
		Err:              &exec.ExitError{ExitCode: 130},
	}, &extensions.Extension{
		Id:      "test.ext",
		Version: "1.0.0",
	})
	require.ErrorIs(t, err, context.Canceled)

	handlers := input.SnapshotInterruptStack()
	require.Len(t, handlers, initialHandlers+1)
	require.True(t, handlers[len(handlers)-1]())
	require.Len(t, input.SnapshotInterruptStack(), initialHandlers)
}

func TestExtensionInterruptController_SuccessAfterObservedSignalWaitsForHostDispatch(t *testing.T) {
	initialHandlers := len(input.SnapshotInterruptStack())
	_, cancel := context.WithCancel(t.Context())
	controller := newExtensionInterruptController(cancel, time.Hour)
	controller.popHandler = input.PushInterruptHandler(controller.handle)
	defer controller.close()

	interruptSignals := make(chan os.Signal, 1)
	observationStopped := false
	controller.interruptSignals = interruptSignals
	controller.stopInterruptObservation = func() {
		observationStopped = true
		interruptSignals <- os.Interrupt
	}

	err := controller.finish(nil, &extensions.Extension{
		Id:      "test.ext",
		Version: "1.0.0",
	})
	require.True(t, observationStopped)
	require.ErrorIs(t, err, context.Canceled)

	handlers := input.SnapshotInterruptStack()
	require.Len(t, handlers, initialHandlers+1)
	require.True(t, handlers[len(handlers)-1]())
	require.Len(t, input.SnapshotInterruptStack(), initialHandlers)
}

func TestExtensionInterruptController_SuccessWithoutObservedSignalPopsHandler(t *testing.T) {
	initialHandlers := len(input.SnapshotInterruptStack())
	_, controller, cleanup := installExtensionInterruptHandler(t.Context())
	defer cleanup()

	require.NoError(t, controller.finish(nil, &extensions.Extension{
		Id:      "test.ext",
		Version: "1.0.0",
	}))
	require.Len(t, input.SnapshotInterruptStack(), initialHandlers)
}

func TestExtensionInterruptController_InterruptExitAfterHandledSignalPopsHandler(t *testing.T) {
	initialHandlers := len(input.SnapshotInterruptStack())
	ctx, cancel := context.WithCancel(t.Context())
	controller := newExtensionInterruptController(cancel, time.Hour)
	controller.popHandler = input.PushInterruptHandler(controller.handle)
	defer controller.close()

	handlers := input.SnapshotInterruptStack()
	require.Len(t, handlers, initialHandlers+1)
	require.True(t, handlers[len(handlers)-1]())
	require.NoError(t, ctx.Err())

	err := controller.finish(&extensions.ExtensionRunError{
		ExtensionId:      "test.ext",
		ExtensionVersion: "1.0.0",
		Err:              &exec.ExitError{ExitCode: 130},
	}, &extensions.Extension{
		Id:      "test.ext",
		Version: "1.0.0",
	})
	require.ErrorIs(t, err, context.Canceled)

	require.Len(t, input.SnapshotInterruptStack(), initialHandlers)
}

func TestExtensionInterruptController_InterruptedDeadlinePreservesDeadline(t *testing.T) {
	initialHandlers := len(input.SnapshotInterruptStack())
	_, controller, cleanup := installExtensionInterruptHandler(t.Context())
	defer cleanup()

	err := controller.finish(&extensions.ExtensionRunError{
		ExtensionId:      "test.ext",
		ExtensionVersion: "1.0.0",
		Err: errors.Join(
			&exec.ExitError{ExitCode: 130},
			context.DeadlineExceeded,
		),
	}, &extensions.Extension{
		Id:      "test.ext",
		Version: "1.0.0",
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NotErrorIs(t, err, context.Canceled)
	require.Len(t, input.SnapshotInterruptStack(), initialHandlers)
}

func TestExtensionInterruptController_FinishedHandlerClaimsOnlyInFlightSignal(t *testing.T) {
	_, cancel := context.WithCancel(t.Context())
	defer cancel()

	controller := newExtensionInterruptController(cancel, time.Hour)
	require.NoError(t, controller.finish(nil, &extensions.Extension{
		Id:      "test.ext",
		Version: "1.0.0",
	}))
	require.True(t, controller.handle())
	require.False(t, controller.handle())
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
			{
				Name:    []string{"default"},
				Aliases: []string{"normal"},
				Subcommands: []extensions.Command{{
					Name:    []string{"default", "local"},
					Aliases: []string{"offline"},
					Launch:  &extensions.CommandLaunch{NoAzdEnvironment: true},
				}},
			},
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
		{[]string{"local", "--debug", "child"}, false},
		{[]string{"conn", "--environment", "add", "list"}, false},
		{[]string{"conn", "--debug=false", "create"}, true},
		{[]string{"conn", "-elist", "create"}, true},
		{[]string{"conn", "-ojson", "list"}, false},
		{[]string{"normal", "--debug", "offline"}, true},
		{[]string{"normal", "--environment=local", "offline"}, true},
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
					secretResolutions := 0
					action.kvService = &mockExecKeyVaultService{
						secretFromKeyVaultRefFn: func(_ context.Context, ref, subscription string) (string, error) {
							secretResolutions++
							require.Equal(t, "akvs://vault/secret", ref)
							require.Equal(t, "test-subscription", subscription)
							return "resolved-value", nil
						},
					}
					action.lazyEnv = lazy.NewLazy(func() (*environment.Environment, error) {
						environmentLoads++
						return environment.NewWithValues("test", map[string]string{
							"EXTENSION_TEST_AZD_ENV": "azd-value",
							"EXTENSION_TEST_KVREF":   "akvs://vault/secret",
							"AZURE_SUBSCRIPTION_ID":  "test-subscription",
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
						require.Zero(t, secretResolutions)
						require.NotContains(t, captured.Env, "EXTENSION_TEST_KVREF=resolved-value")
						require.NotContains(t, captured.Env, "EXTENSION_TEST_AZD_ENV=azd-value")
					} else {
						require.Equal(t, 1, environmentLoads)
						require.Equal(t, 1, secretResolutions)
						require.Contains(t, captured.Env, "EXTENSION_TEST_KVREF=resolved-value")
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

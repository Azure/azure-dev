// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	contracts "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
)

type listenerCancellationCommandRunner struct {
	started chan struct{}
	stopped chan struct{}
}

func (r *listenerCancellationCommandRunner) Run(
	ctx context.Context,
	args exec.RunArgs,
) (exec.RunResult, error) {
	close(r.started)
	<-ctx.Done()
	close(r.stopped)
	return exec.NewRunResult(0, "", ""), nil
}

func (r *listenerCancellationCommandRunner) RunList(
	context.Context,
	[]string,
	exec.RunArgs,
) (exec.RunResult, error) {
	panic("unexpected RunList call")
}

func (r *listenerCancellationCommandRunner) ToolInPath(string) error {
	return nil
}

func newExtensionsMiddlewareTestServer() *grpcserver.Server {
	return grpcserver.NewServer(
		azdext.UnimplementedProjectServiceServer{},
		azdext.UnimplementedEnvironmentServiceServer{},
		azdext.UnimplementedPromptServiceServer{},
		azdext.UnimplementedUserConfigServiceServer{},
		azdext.UnimplementedDeploymentServiceServer{},
		azdext.UnimplementedEventServiceServer{},
		contracts.UnimplementedComposeServiceServer{},
		azdext.UnimplementedWorkflowServiceServer{},
		azdext.UnimplementedExtensionServiceServer{},
		azdext.UnimplementedServiceTargetServiceServer{},
		azdext.UnimplementedFrameworkServiceServer{},
		azdext.UnimplementedContainerServiceServer{},
		azdext.UnimplementedAccountServiceServer{},
		azdext.UnimplementedAiModelServiceServer{},
		contracts.UnimplementedCopilotServiceServer{},
		azdext.UnimplementedProvisioningServiceServer{},
		azdext.UnimplementedValidationServiceServer{},
		contracts.UnimplementedTelemetryServiceServer{},
		contracts.UnimplementedCommandResultServiceServer{},
	)
}

func TestStartAndWaitExtension_PropagatesTraceContext(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)

	extensionPath := filepath.Join("extensions", "test-ext", "bin", "test-ext")
	fullPath := filepath.Join(configDir, extensionPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
	require.NoError(t, os.WriteFile(fullPath, []byte("test"), 0o600))

	mockCtx := mocks.NewMockContext(t.Context())
	var captured exec.RunArgs
	listenErr := errors.New("listen failed")
	mockCtx.CommandRunner.When(func(args exec.RunArgs, _ string) bool {
		captured = args
		return true
	}).SetError(listenErr)

	traceparent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	ctx := propagation.TraceContext{}.Extract(t.Context(), propagation.MapCarrier{
		"traceparent": traceparent,
		"tracestate":  "vendor=value",
	})
	extension := &extensions.Extension{
		Id:   "test-ext",
		Path: extensionPath,
	}
	serverInfo := &grpcserver.ServerInfo{
		Address:    "127.0.0.1:1234",
		SigningKey: []byte("01234567890123456789012345678901"),
	}

	process, err := startAndWaitExtension(
		ctx,
		ctx,
		extension,
		extensions.NewRunner(mockCtx.CommandRunner),
		serverInfo,
		extensionStartOptions{
			debug:       true,
			cwd:         "work",
			environment: "test",
			forceColor:  true,
			noPrompt:    true,
		},
	)

	require.ErrorIs(t, err, listenErr)
	require.NotNil(t, process)
	<-process.done
	require.Equal(t, fullPath, captured.Cmd)
	require.Equal(t, []string{"listen", "--debug"}, captured.Args)
	require.Contains(t, captured.Env, "AZD_SERVER=127.0.0.1:1234")
	require.Contains(t, captured.Env, "FORCE_COLOR=1")
	require.Contains(t, captured.Env, "TRACEPARENT="+traceparent)
	require.Contains(t, captured.Env, "TRACESTATE=vendor=value")
	require.Contains(t, captured.Env, "AZD_DEBUG=true")
	require.Contains(t, captured.Env, "AZD_NO_PROMPT=true")
	require.Contains(t, captured.Env, "AZD_CWD=work")
	require.Contains(t, captured.Env, "AZD_ENVIRONMENT=test")
}

func TestExtensionsMiddleware_Run_ContinuesAfterExtensionStartFailure(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)

	extensionPath := filepath.Join("extensions", "test-ext", "bin", "test-ext")
	fullPath := filepath.Join(configDir, extensionPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
	require.NoError(t, os.WriteFile(fullPath, []byte("test"), 0o600))

	mockCtx := mocks.NewMockContext(t.Context())
	extension := &extensions.Extension{
		Id:           "test-ext",
		Path:         extensionPath,
		Capabilities: []extensions.CapabilityType{extensions.LifecycleEventsCapability},
	}
	manager := createExtensionsManager(
		t,
		mockCtx,
		map[string]*extensions.Extension{extension.Id: extension},
	)
	startErr := errors.New("listen failed")
	mockCtx.CommandRunner.When(func(exec.RunArgs, string) bool {
		return true
	}).SetError(startErr)
	ioc.RegisterInstance[*grpcserver.Server](
		mockCtx.Container,
		newExtensionsMiddlewareTestServer(),
	)

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Bool("debug", true, "")
	flags.String("cwd", "work", "")
	flags.String("environment", "test", "")
	middleware := &ExtensionsMiddleware{
		extensionManager: manager,
		extensionRunner:  extensions.NewRunner(mockCtx.CommandRunner),
		serviceLocator:   mockCtx.Container,
		console:          mockCtx.Console,
		options:          &Options{Flags: flags},
		globalOptions:    &internal.GlobalCommandOptions{NoPrompt: true},
	}
	next, calls := nextCounter()

	result, err := middleware.Run(t.Context(), next)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, *calls)
}

func TestShutdownExtensionProcesses_AutomaticallyCancelsUnresponsiveListener(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)
	extensionPath := filepath.Join("extensions", "test-ext", "bin", "test-ext")
	fullPath := filepath.Join(configDir, extensionPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
	require.NoError(t, os.WriteFile(fullPath, []byte("test"), 0o600))

	runner := &listenerCancellationCommandRunner{
		started: make(chan struct{}),
		stopped: make(chan struct{}),
	}
	extension := &extensions.Extension{
		Id:   "test-ext",
		Path: extensionPath,
	}
	extension.Initialize()

	server := newExtensionsMiddlewareTestServer()
	serverInfo, err := server.Start()
	require.NoError(t, err)

	processCtx, cancelProcess := context.WithCancel(context.WithoutCancel(t.Context()))
	process, err := startAndWaitExtension(
		t.Context(),
		processCtx,
		extension,
		extensions.NewRunner(runner),
		serverInfo,
		extensionStartOptions{},
	)
	require.NoError(t, err)
	require.NotNil(t, process)

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("listener process did not start")
	}

	shutdownExtensionProcessesWithGracePeriod(
		server,
		cancelProcess,
		[]*extensionProcess{process},
		20*time.Millisecond,
	)

	select {
	case <-runner.stopped:
	case <-time.After(time.Second):
		t.Fatal("listener process was not canceled after the shutdown grace period")
	}
}

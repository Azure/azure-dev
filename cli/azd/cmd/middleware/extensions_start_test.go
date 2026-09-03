// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

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
		azdext.UnimplementedTelemetryServiceServer{},
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

	err := startAndWaitExtension(
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

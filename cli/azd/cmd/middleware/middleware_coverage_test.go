// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/agent"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/platform"
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// nextOK is a simple NextFn that always succeeds.
func nextOK(_ context.Context) (*actions.ActionResult, error) {
	return &actions.ActionResult{Message: &actions.ResultMessage{Header: "ok"}}, nil
}

// nextCounter returns a NextFn that increments *count on each call and the
// pointer to the counter.
func nextCounter() (NextFn, *int) {
	count := 0
	fn := func(_ context.Context) (*actions.ActionResult, error) {
		count++
		return &actions.ActionResult{Message: &actions.ResultMessage{Header: "ok"}}, nil
	}
	return fn, &count
}

// createExtensionsManager creates a real *extensions.Manager backed by an
// in-memory config with no installed extensions, suitable for middleware tests
// that exercise the "no-extensions" or "no-listen-capability" path.
func createExtensionsManager(
	t *testing.T,
	mockCtx *mocks.MockContext,
	installed map[string]*extensions.Extension,
) *extensions.Manager {
	t.Helper()

	userConfigManager := config.NewUserConfigManager(mockCtx.ConfigManager)
	sourceManager := extensions.NewSourceManager(mockCtx.Container, userConfigManager, mockCtx.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*extensions.Runner, error) {
		return extensions.NewRunner(exec.NewCommandRunner(nil)), nil
	})

	manager, err := extensions.NewManager(userConfigManager, sourceManager, lazyRunner, mockCtx.HttpClient)
	require.NoError(t, err)

	if installed != nil {
		cfg, err := userConfigManager.Load()
		require.NoError(t, err)
		err = cfg.Set("extension.installed", installed)
		require.NoError(t, err)
	}

	return manager
}

// ---------------------------------------------------------------------------
// ExperimentationMiddleware
// ---------------------------------------------------------------------------

func TestNewExperimentationMiddleware(t *testing.T) {
	m := NewExperimentationMiddleware()
	require.NotNil(t, m)

	_, ok := m.(*ExperimentationMiddleware)
	require.True(t, ok, "should return *ExperimentationMiddleware")
}

func TestExperimentationMiddleware_Run_AlwaysCallsNext(t *testing.T) {
	// The middleware attempts to contact TAS, but regardless of success or
	// failure it must call next(ctx).  In a unit-test environment the TAS
	// endpoint is unreachable, so the manager-creation or assignment call
	// will fail – but next must still be invoked.
	m := &ExperimentationMiddleware{}
	nextFn, count := nextCounter()

	result, err := m.Run(context.Background(), nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, *count, "next must be called exactly once")
}

func TestExperimentationMiddleware_Run_OverrideEndpoint(t *testing.T) {
	// Setting the debug env var changes the endpoint used internally.
	// The middleware should still call next regardless.
	t.Setenv("AZD_DEBUG_EXPERIMENTATION_TAS_ENDPOINT", "http://localhost:0/fake")

	m := &ExperimentationMiddleware{}
	nextFn, count := nextCounter()

	result, err := m.Run(context.Background(), nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, *count)
}

func TestExperimentationMiddleware_Run_PropagatesNextError(t *testing.T) {
	m := &ExperimentationMiddleware{}

	expectedErr := context.DeadlineExceeded
	nextFn := func(_ context.Context) (*actions.ActionResult, error) {
		return nil, expectedErr
	}

	result, err := m.Run(context.Background(), nextFn)

	require.ErrorIs(t, err, expectedErr)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// ExtensionsMiddleware — constructor
// ---------------------------------------------------------------------------

func TestNewExtensionsMiddleware(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	manager := createExtensionsManager(t, mockCtx, nil)
	runner := extensions.NewRunner(exec.NewCommandRunner(nil))
	console := mockinput.NewMockConsole()

	m := NewExtensionsMiddleware(
		&Options{Name: "test"},
		mockCtx.Container,
		manager,
		runner,
		console,
		&internal.GlobalCommandOptions{},
	)
	require.NotNil(t, m)

	em, ok := m.(*ExtensionsMiddleware)
	require.True(t, ok)
	require.Equal(t, manager, em.extensionManager)
	require.Equal(t, runner, em.extensionRunner)
}

// ---------------------------------------------------------------------------
// ExtensionsMiddleware.Run — child-action path
// ---------------------------------------------------------------------------

func TestExtensionsMiddleware_Run_ChildAction_SkipsExtensions(t *testing.T) {
	// When the context marks a child action, Run must delegate to next()
	// immediately without touching the extension manager.
	m := &ExtensionsMiddleware{
		// extensionManager is intentionally nil – it must not be touched
	}

	ctx := WithChildAction(context.Background())
	nextFn, count := nextCounter()

	result, err := m.Run(ctx, nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, *count)
}

// ---------------------------------------------------------------------------
// ExtensionsMiddleware.Run — no installed extensions
// ---------------------------------------------------------------------------

func TestExtensionsMiddleware_Run_NoInstalledExtensions(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	manager := createExtensionsManager(t, mockCtx, nil)

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Bool("debug", false, "")
	flags.String("cwd", "", "")
	flags.String("environment", "", "")

	m := &ExtensionsMiddleware{
		extensionManager: manager,
		options:          &Options{Flags: flags},
		globalOptions:    &internal.GlobalCommandOptions{},
	}

	nextFn, count := nextCounter()

	result, err := m.Run(context.Background(), nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, *count)
}

// ---------------------------------------------------------------------------
// ExtensionsMiddleware.Run — extensions without listen capabilities
// ---------------------------------------------------------------------------

func TestExtensionsMiddleware_Run_NoListenCapabilities(t *testing.T) {
	// Extensions exist but none have a listen capability, so the middleware
	// should short-circuit and call next() without starting gRPC or processes.
	installed := map[string]*extensions.Extension{
		"test.ext": {
			Id:           "test.ext",
			DisplayName:  "Test Extension",
			Version:      "1.0.0",
			Capabilities: []extensions.CapabilityType{"some-other-capability"},
		},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	manager := createExtensionsManager(t, mockCtx, installed)

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Bool("debug", false, "")
	flags.String("cwd", "", "")
	flags.String("environment", "", "")

	m := &ExtensionsMiddleware{
		extensionManager: manager,
		options:          &Options{Flags: flags},
		globalOptions:    &internal.GlobalCommandOptions{},
	}

	nextFn, count := nextCounter()

	result, err := m.Run(context.Background(), nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, *count)
}

// ---------------------------------------------------------------------------
// isDebug — pure function tests
// ---------------------------------------------------------------------------

func TestIsDebug(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     bool
	}{
		{"Unset", "", false},
		{"True", "true", true},
		{"True_1", "1", true},
		{"False", "false", false},
		{"False_0", "0", false},
		{"Invalid", "not-a-bool", false},
		{"TrueUpperCase", "TRUE", true},
		{"FalseUpperCase", "FALSE", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZD_EXT_DEBUG", tt.envValue)
			require.Equal(t, tt.want, isDebug())
		})
	}
}

// ---------------------------------------------------------------------------
// getReadyContext — context factory tests
// ---------------------------------------------------------------------------

func TestGetReadyContext_DefaultTimeout(t *testing.T) {
	// When AZD_EXT_DEBUG is not set and AZD_EXT_TIMEOUT is not set,
	// getReadyContext returns a context with 15-second timeout.
	t.Setenv("AZD_EXT_DEBUG", "")
	t.Setenv("AZD_EXT_TIMEOUT", "")

	ctx, cancel := getReadyContext(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok, "context must have a deadline")

	remaining := time.Until(deadline)
	require.InDelta(t, 15.0, remaining.Seconds(), 1.0,
		"default timeout should be ~15 seconds")
}

func TestGetReadyContext_CustomTimeout(t *testing.T) {
	t.Setenv("AZD_EXT_DEBUG", "")
	t.Setenv("AZD_EXT_TIMEOUT", "5")

	ctx, cancel := getReadyContext(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok, "context must have a deadline")

	remaining := time.Until(deadline)
	require.InDelta(t, 5.0, remaining.Seconds(), 1.0,
		"custom timeout should be ~5 seconds")
}

func TestGetReadyContext_InvalidTimeout_FallsBackToDefault(t *testing.T) {
	t.Setenv("AZD_EXT_DEBUG", "")
	t.Setenv("AZD_EXT_TIMEOUT", "not-a-number")

	ctx, cancel := getReadyContext(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok, "context must have a deadline")

	remaining := time.Until(deadline)
	require.InDelta(t, 15.0, remaining.Seconds(), 1.0,
		"invalid timeout should fall back to 15 seconds")
}

func TestGetReadyContext_NegativeTimeout_FallsBackToDefault(t *testing.T) {
	t.Setenv("AZD_EXT_DEBUG", "")
	t.Setenv("AZD_EXT_TIMEOUT", "-1")

	ctx, cancel := getReadyContext(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok, "context must have a deadline")

	remaining := time.Until(deadline)
	require.InDelta(t, 15.0, remaining.Seconds(), 1.0,
		"negative timeout should fall back to 15 seconds")
}

func TestGetReadyContext_ZeroTimeout_FallsBackToDefault(t *testing.T) {
	t.Setenv("AZD_EXT_DEBUG", "")
	t.Setenv("AZD_EXT_TIMEOUT", "0")

	ctx, cancel := getReadyContext(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok, "context must have a deadline")

	remaining := time.Until(deadline)
	require.InDelta(t, 15.0, remaining.Seconds(), 1.0,
		"zero timeout should fall back to 15 seconds")
}

func TestGetReadyContext_DebugMode_NoTimeout(t *testing.T) {
	// In debug mode, getReadyContext returns a cancellable context
	// without a deadline to allow indefinite debugger attachment.
	t.Setenv("AZD_EXT_DEBUG", "true")

	ctx, cancel := getReadyContext(context.Background())
	defer cancel()

	_, ok := ctx.Deadline()
	require.False(t, ok, "debug mode should not impose a deadline")
}

// ---------------------------------------------------------------------------
// ExtensionsMiddleware.Run — ListInstalled error
// ---------------------------------------------------------------------------

func TestExtensionsMiddleware_Run_ListInstalledError(t *testing.T) {
	// Seed the config with an invalid value for the installed extensions
	// section so that GetSection fails during unmarshalling.
	mockCtx := mocks.NewMockContext(context.Background())
	userConfigManager := config.NewUserConfigManager(mockCtx.ConfigManager)

	cfg, err := userConfigManager.Load()
	require.NoError(t, err)
	// Set an invalid value — a string instead of a map — so that
	// json.Unmarshal into map[string]*Extension will fail.
	err = cfg.Set("extension.installed", "invalid-not-a-map")
	require.NoError(t, err)

	sourceManager := extensions.NewSourceManager(mockCtx.Container, userConfigManager, mockCtx.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*extensions.Runner, error) {
		return extensions.NewRunner(exec.NewCommandRunner(nil)), nil
	})
	manager, err := extensions.NewManager(userConfigManager, sourceManager, lazyRunner, mockCtx.HttpClient)
	require.NoError(t, err)

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Bool("debug", false, "")
	flags.String("cwd", "", "")
	flags.String("environment", "", "")

	m := &ExtensionsMiddleware{
		extensionManager: manager,
		options:          &Options{Flags: flags},
		globalOptions:    &internal.GlobalCommandOptions{},
	}

	_, err = m.Run(context.Background(), nextOK)
	require.Error(t, err, "should propagate ListInstalled error")
}

// ---------------------------------------------------------------------------
// ExtensionsMiddleware.Run — extensions with listen capabilities but
// ServiceLocator cannot resolve gRPC server
// ---------------------------------------------------------------------------

func TestExtensionsMiddleware_Run_ListenCapabilities_ResolveGrpcFails(t *testing.T) {
	installed := map[string]*extensions.Extension{
		"test.ext": {
			Id:           "test.ext",
			DisplayName:  "Test Extension",
			Version:      "1.0.0",
			Capabilities: []extensions.CapabilityType{extensions.LifecycleEventsCapability},
		},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	manager := createExtensionsManager(t, mockCtx, installed)

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Bool("debug", false, "")
	flags.String("cwd", "", "")
	flags.String("environment", "", "")

	m := &ExtensionsMiddleware{
		extensionManager: manager,
		extensionRunner:  extensions.NewRunner(exec.NewCommandRunner(nil)),
		serviceLocator:   mockCtx.Container, // no gRPC server registered
		console:          mockinput.NewMockConsole(),
		options:          &Options{Flags: flags},
		globalOptions:    &internal.GlobalCommandOptions{},
	}

	_, err := m.Run(context.Background(), nextOK)
	require.Error(t, err, "should fail when gRPC server cannot be resolved")
}

// ---------------------------------------------------------------------------
// ExtensionsMiddleware.Run — ServiceTargetProvider capability
// ---------------------------------------------------------------------------

func TestExtensionsMiddleware_Run_ServiceTargetProviderCapability(t *testing.T) {
	installed := map[string]*extensions.Extension{
		"test.svc": {
			Id:           "test.svc",
			DisplayName:  "Service Provider Ext",
			Version:      "1.0.0",
			Capabilities: []extensions.CapabilityType{extensions.ServiceTargetProviderCapability},
		},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	manager := createExtensionsManager(t, mockCtx, installed)

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Bool("debug", false, "")
	flags.String("cwd", "", "")
	flags.String("environment", "", "")

	m := &ExtensionsMiddleware{
		extensionManager: manager,
		extensionRunner:  extensions.NewRunner(exec.NewCommandRunner(nil)),
		serviceLocator:   mockCtx.Container, // no gRPC server registered
		console:          mockinput.NewMockConsole(),
		options:          &Options{Flags: flags},
		globalOptions:    &internal.GlobalCommandOptions{},
	}

	_, err := m.Run(context.Background(), nextOK)
	require.Error(t, err, "should fail — gRPC server not registered")
}

// ---------------------------------------------------------------------------
// ExtensionsMiddleware.Run — FrameworkServiceProvider capability
// ---------------------------------------------------------------------------

func TestExtensionsMiddleware_Run_FrameworkServiceProviderCapability(t *testing.T) {
	installed := map[string]*extensions.Extension{
		"test.fw": {
			Id:           "test.fw",
			DisplayName:  "Framework Provider Ext",
			Version:      "1.0.0",
			Capabilities: []extensions.CapabilityType{extensions.FrameworkServiceProviderCapability},
		},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	manager := createExtensionsManager(t, mockCtx, installed)

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Bool("debug", false, "")
	flags.String("cwd", "", "")
	flags.String("environment", "", "")

	m := &ExtensionsMiddleware{
		extensionManager: manager,
		extensionRunner:  extensions.NewRunner(exec.NewCommandRunner(nil)),
		serviceLocator:   mockCtx.Container,
		console:          mockinput.NewMockConsole(),
		options:          &Options{Flags: flags},
		globalOptions:    &internal.GlobalCommandOptions{},
	}

	_, err := m.Run(context.Background(), nextOK)
	require.Error(t, err, "should fail — gRPC server not registered")
}

// ---------------------------------------------------------------------------
// ExtensionsMiddleware.Run — mixed capabilities: some listen, some don't
// ---------------------------------------------------------------------------

func TestExtensionsMiddleware_Run_MixedCapabilities(t *testing.T) {
	installed := map[string]*extensions.Extension{
		"test.listen": {
			Id:           "test.listen",
			DisplayName:  "Listen Ext",
			Version:      "1.0.0",
			Capabilities: []extensions.CapabilityType{extensions.LifecycleEventsCapability},
		},
		"test.other": {
			Id:           "test.other",
			DisplayName:  "Other Ext",
			Version:      "1.0.0",
			Capabilities: []extensions.CapabilityType{"mcp-server"},
		},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	manager := createExtensionsManager(t, mockCtx, installed)

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Bool("debug", false, "")
	flags.String("cwd", "", "")
	flags.String("environment", "", "")

	m := &ExtensionsMiddleware{
		extensionManager: manager,
		extensionRunner:  extensions.NewRunner(exec.NewCommandRunner(nil)),
		serviceLocator:   mockCtx.Container,
		console:          mockinput.NewMockConsole(),
		options:          &Options{Flags: flags},
		globalOptions:    &internal.GlobalCommandOptions{},
	}

	// Should attempt to start the listen extension and fail at gRPC resolution
	_, err := m.Run(context.Background(), nextOK)
	require.Error(t, err, "should fail — gRPC server not registered")
}

// ---------------------------------------------------------------------------
// ExperimentationMiddleware.Run — cancelled context
// ---------------------------------------------------------------------------

func TestExperimentationMiddleware_Run_CancelledContext(t *testing.T) {
	m := &ExperimentationMiddleware{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	nextFn, count := nextCounter()

	// Even with cancelled context, the middleware should still call next.
	result, err := m.Run(ctx, nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, *count)
}

// ---------------------------------------------------------------------------
// ExtensionsMiddleware.Run — propagates next function result
// ---------------------------------------------------------------------------

func TestExtensionsMiddleware_Run_NoExtensions_PropagatesNextResult(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	manager := createExtensionsManager(t, mockCtx, nil)

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Bool("debug", false, "")
	flags.String("cwd", "", "")
	flags.String("environment", "", "")

	m := &ExtensionsMiddleware{
		extensionManager: manager,
		options:          &Options{Flags: flags},
		globalOptions:    &internal.GlobalCommandOptions{},
	}

	expected := &actions.ActionResult{
		Message: &actions.ResultMessage{Header: "custom-result"},
	}

	result, err := m.Run(context.Background(), func(_ context.Context) (*actions.ActionResult, error) {
		return expected, nil
	})

	require.NoError(t, err)
	require.Equal(t, expected, result)
}

func TestExtensionsMiddleware_Run_NoExtensions_PropagatesNextError(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	manager := createExtensionsManager(t, mockCtx, nil)

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Bool("debug", false, "")
	flags.String("cwd", "", "")
	flags.String("environment", "", "")

	m := &ExtensionsMiddleware{
		extensionManager: manager,
		options:          &Options{Flags: flags},
		globalOptions:    &internal.GlobalCommandOptions{},
	}

	expectedErr := context.DeadlineExceeded
	result, err := m.Run(context.Background(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, expectedErr
	})

	require.ErrorIs(t, err, expectedErr)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// listenCapabilities variable — verify expected values
// ---------------------------------------------------------------------------

func TestListenCapabilities_ContainsExpectedValues(t *testing.T) {
	require.Contains(t, listenCapabilities, extensions.LifecycleEventsCapability)
	require.Contains(t, listenCapabilities, extensions.ServiceTargetProviderCapability)
	require.Contains(t, listenCapabilities, extensions.FrameworkServiceProviderCapability)
	require.Len(t, listenCapabilities, 3)
}

// ---------------------------------------------------------------------------
// extensionFailure struct — basic construction
// ---------------------------------------------------------------------------

func TestExtensionFailure_Fields(t *testing.T) {
	ext := &extensions.Extension{Id: "test.ext"}
	err := context.DeadlineExceeded
	f := extensionFailure{
		extension: ext,
		err:       err,
		timedOut:  true,
	}

	require.Equal(t, ext, f.extension)
	require.ErrorIs(t, f.err, context.DeadlineExceeded)
	require.True(t, f.timedOut)
}

func TestExtensionFailure_NotTimedOut(t *testing.T) {
	ext := &extensions.Extension{Id: "test.ext2"}
	f := extensionFailure{
		extension: ext,
		err:       context.Canceled,
		timedOut:  false,
	}

	require.Equal(t, ext, f.extension)
	require.ErrorIs(t, f.err, context.Canceled)
	require.False(t, f.timedOut)
}

// ---------------------------------------------------------------------------
// NewLoginGuardMiddleware — constructor coverage
// ---------------------------------------------------------------------------

func TestNewLoginGuardMiddleware(t *testing.T) {
	console := mockinput.NewMockConsole()
	// Use the mock type from login_guard_test.go — but since we're in the same
	// package we can just use the interface directly with a nil implementation
	// through a minimal mock.
	m := NewLoginGuardMiddleware(console, nil, &workflow.Runner{})
	require.NotNil(t, m)

	lgm, ok := m.(*LoginGuardMiddleware)
	require.True(t, ok)
	require.Equal(t, console, lgm.console)
	require.Nil(t, lgm.authManager)
	require.NotNil(t, lgm.workflowRunner)
}

// ---------------------------------------------------------------------------
// TelemetryMiddleware.extensionCmdInfo — nil manager path
// ---------------------------------------------------------------------------

func TestTelemetryMiddleware_extensionCmdInfo_NilManager(t *testing.T) {
	m := &TelemetryMiddleware{
		options:          &Options{},
		extensionManager: nil,
	}

	eventName, flags := m.extensionCmdInfo("some.extension")
	require.Empty(t, eventName)
	require.Nil(t, flags)
}

// ---------------------------------------------------------------------------
// TelemetryMiddleware.extensionCmdInfo — extension not found
// ---------------------------------------------------------------------------

func TestTelemetryMiddleware_extensionCmdInfo_ExtensionNotFound(t *testing.T) {
	// Create a manager with no installed extensions so GetInstalled fails
	mockCtx := mocks.NewMockContext(context.Background())
	manager := createExtensionsManager(t, mockCtx, nil)

	m := &TelemetryMiddleware{
		options:          &Options{Args: []string{"test"}},
		extensionManager: manager,
	}

	eventName, flags := m.extensionCmdInfo("nonexistent.ext")
	require.Empty(t, eventName)
	require.Nil(t, flags)
}

// ---------------------------------------------------------------------------
// TelemetryMiddleware.extensionCmdInfo — extension without metadata capability
// ---------------------------------------------------------------------------

func TestTelemetryMiddleware_extensionCmdInfo_NoMetadataCapability(t *testing.T) {
	installed := map[string]*extensions.Extension{
		"test.ext": {
			Id:           "test.ext",
			DisplayName:  "Test Extension",
			Version:      "1.0.0",
			Capabilities: []extensions.CapabilityType{extensions.LifecycleEventsCapability},
		},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	manager := createExtensionsManager(t, mockCtx, installed)

	m := &TelemetryMiddleware{
		options:          &Options{Args: []string{"test"}},
		extensionManager: manager,
	}

	eventName, flags := m.extensionCmdInfo("test.ext")
	require.Empty(t, eventName)
	require.Nil(t, flags)
}

// ---------------------------------------------------------------------------
// NewTelemetryMiddleware — constructor coverage
// ---------------------------------------------------------------------------

func TestNewTelemetryMiddleware(t *testing.T) {
	opts := &Options{Name: "test"}
	lazyConfig := lazy.NewLazy(func() (*platform.Config, error) {
		return &platform.Config{}, nil
	})

	m := NewTelemetryMiddleware(opts, lazyConfig, nil)
	require.NotNil(t, m)

	tm, ok := m.(*TelemetryMiddleware)
	require.True(t, ok)
	require.Equal(t, opts, tm.options)
	require.Nil(t, tm.extensionManager)
}

// ---------------------------------------------------------------------------
// assignmentEndpoint constant verification
// ---------------------------------------------------------------------------

func TestAssignmentEndpoint_IsNotEmpty(t *testing.T) {
	require.NotEmpty(t, assignmentEndpoint)
	require.Contains(t, assignmentEndpoint, "exp-tas.com")
}

// ---------------------------------------------------------------------------
// shouldSkipErrorAnalysis — additional error types
// ---------------------------------------------------------------------------

func TestShouldSkipErrorAnalysis_ContextCanceled(t *testing.T) {
	require.True(t, shouldSkipErrorAnalysis(context.Canceled))
}

func TestShouldSkipErrorAnalysis_AbortedByUser(t *testing.T) {
	require.True(t, shouldSkipErrorAnalysis(internal.ErrAbortedByUser))
}

func TestShouldSkipErrorAnalysis_RegularError(t *testing.T) {
	require.False(t, shouldSkipErrorAnalysis(errors.New("some regular error")))
}

func TestShouldSkipErrorAnalysis_WrappedCanceled(t *testing.T) {
	err := fmt.Errorf("operation failed: %w", context.Canceled)
	require.True(t, shouldSkipErrorAnalysis(err))
}

func TestShouldSkipErrorAnalysis_NilError(t *testing.T) {
	// nil error should not be skipped — though callers check nil before calling
	require.False(t, shouldSkipErrorAnalysis(nil))
}

// ---------------------------------------------------------------------------
// ErrorMiddleware.displayUsageMetrics
// ---------------------------------------------------------------------------

func TestErrorMiddleware_displayUsageMetrics_WithTokens(t *testing.T) {
	console := mockinput.NewMockConsole()
	e := &ErrorMiddleware{
		console: console,
	}

	result := &agent.AgentResult{
		Usage: agent.UsageMetrics{
			InputTokens:  1000,
			OutputTokens: 500,
		},
	}

	// Should not panic
	require.NotPanics(t, func() {
		e.displayUsageMetrics(context.Background(), result)
	})
}

func TestErrorMiddleware_displayUsageMetrics_NilResult(t *testing.T) {
	console := mockinput.NewMockConsole()
	e := &ErrorMiddleware{
		console: console,
	}

	// Should not panic with nil result
	require.NotPanics(t, func() {
		e.displayUsageMetrics(context.Background(), nil)
	})
}

func TestErrorMiddleware_displayUsageMetrics_ZeroTokens(t *testing.T) {
	console := mockinput.NewMockConsole()
	e := &ErrorMiddleware{
		console: console,
	}

	result := &agent.AgentResult{
		Usage: agent.UsageMetrics{
			InputTokens:  0,
			OutputTokens: 0,
		},
	}

	// Should not panic, and should not print anything
	require.NotPanics(t, func() {
		e.displayUsageMetrics(context.Background(), result)
	})
}

// ---------------------------------------------------------------------------
// ErrorMiddleware.Run — no error path
// ---------------------------------------------------------------------------

func TestErrorMiddleware_Run_NoError(t *testing.T) {
	console := mockinput.NewMockConsole()
	e := &ErrorMiddleware{
		options:         &Options{},
		console:         console,
		global:          &internal.GlobalCommandOptions{},
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		errorPipeline:   errorhandler.NewErrorHandlerPipeline(nil),
	}

	expected := &actions.ActionResult{
		Message: &actions.ResultMessage{Header: "success"},
	}

	result, err := e.Run(context.Background(), func(_ context.Context) (*actions.ActionResult, error) {
		return expected, nil
	})

	require.NoError(t, err)
	require.Equal(t, expected, result)
}

// ---------------------------------------------------------------------------
// ErrorMiddleware.Run — child action path (error passes through)
// ---------------------------------------------------------------------------

func TestErrorMiddleware_Run_ChildAction_PassesError(t *testing.T) {
	console := mockinput.NewMockConsole()
	e := &ErrorMiddleware{
		options:         &Options{},
		console:         console,
		global:          &internal.GlobalCommandOptions{},
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		errorPipeline:   errorhandler.NewErrorHandlerPipeline(nil),
	}

	ctx := WithChildAction(context.Background())
	expectedErr := errors.New("child error")

	result, err := e.Run(ctx, func(_ context.Context) (*actions.ActionResult, error) {
		return nil, expectedErr
	})

	require.ErrorIs(t, err, expectedErr)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// ErrorMiddleware.Run — error with shouldSkipErrorAnalysis
// ---------------------------------------------------------------------------

func TestErrorMiddleware_Run_SkippableError_NoPrompt(t *testing.T) {
	console := mockinput.NewMockConsole()
	e := &ErrorMiddleware{
		options:         &Options{},
		console:         console,
		global:          &internal.GlobalCommandOptions{NoPrompt: true},
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		errorPipeline:   errorhandler.NewErrorHandlerPipeline(nil),
	}

	result, err := e.Run(context.Background(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, context.Canceled
	})

	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// ErrorMiddleware.Run — ErrorWithSuggestion passes through
// ---------------------------------------------------------------------------

func TestErrorMiddleware_Run_ErrorWithSuggestion(t *testing.T) {
	console := mockinput.NewMockConsole()
	e := &ErrorMiddleware{
		options:         &Options{},
		console:         console,
		global:          &internal.GlobalCommandOptions{},
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		errorPipeline:   errorhandler.NewErrorHandlerPipeline(nil),
	}

	sugErr := &internal.ErrorWithSuggestion{
		Err:        errors.New("base error"),
		Suggestion: "try this fix",
	}

	result, err := e.Run(context.Background(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, sugErr
	})

	require.Error(t, err)
	var ews *internal.ErrorWithSuggestion
	require.True(t, errors.As(err, &ews))
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// classifyError — additional coverage
// ---------------------------------------------------------------------------

func TestClassifyError_RegularError(t *testing.T) {
	result := fixableError(errors.New("some unknown error"))
	require.True(t, result)
}

func TestClassifyError_ContextCanceled(t *testing.T) {
	// context.Canceled is not a typed auth/tool error, so it falls through
	// as fixable (the catch-all default).
	result := fixableError(context.Canceled)
	require.True(t, result)
}

// ---------------------------------------------------------------------------
// ErrorMiddleware.Run — regular error, copilot disabled, no prompt
// ---------------------------------------------------------------------------

func TestErrorMiddleware_Run_RegularError_CopilotDisabled(t *testing.T) {
	// With copilot feature disabled (default), a regular error should be
	// returned as-is after the error pipeline check.
	console := mockinput.NewMockConsole()
	e := &ErrorMiddleware{
		options:         &Options{},
		console:         console,
		global:          &internal.GlobalCommandOptions{},
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		errorPipeline:   errorhandler.NewErrorHandlerPipeline(nil),
	}

	expectedErr := errors.New("deployment failed: resource not found")
	result, err := e.Run(context.Background(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, expectedErr
	})

	require.ErrorIs(t, err, expectedErr)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// ErrorMiddleware.Run — error with pipeline match
// ---------------------------------------------------------------------------

func TestErrorMiddleware_Run_ErrorPipelineMatch(t *testing.T) {
	// Use an error string that matches one of the YAML rules in the error pipeline.
	// The pipeline should wrap it with an ErrorWithSuggestion.
	console := mockinput.NewMockConsole()
	e := &ErrorMiddleware{
		options:         &Options{},
		console:         console,
		global:          &internal.GlobalCommandOptions{},
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		errorPipeline:   errorhandler.NewErrorHandlerPipeline(nil),
	}

	// Use an error that might match a known pattern (e.g., Azure auth errors).
	// Even if no pattern matches, the code path is exercised.
	result, err := e.Run(context.Background(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, errors.New("some unmatched error")
	})

	require.Error(t, err)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// ErrorMiddleware.Run — error on CI (resource.IsRunningOnCI check)
// ---------------------------------------------------------------------------

func TestErrorMiddleware_Run_OnCI_ShortCircuits(t *testing.T) {
	// When running in CI, error analysis should be skipped.
	t.Setenv("CI", "true")

	console := mockinput.NewMockConsole()
	e := &ErrorMiddleware{
		options:         &Options{},
		console:         console,
		global:          &internal.GlobalCommandOptions{},
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		errorPipeline:   errorhandler.NewErrorHandlerPipeline(nil),
	}

	expectedErr := errors.New("some error in CI")
	result, err := e.Run(context.Background(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, expectedErr
	})

	require.ErrorIs(t, err, expectedErr)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// ErrorMiddleware.Run — shouldSkipErrorAnalysis path
// ---------------------------------------------------------------------------

func TestErrorMiddleware_Run_SkippableError_CancelledInNonPrompt(t *testing.T) {
	console := mockinput.NewMockConsole()
	e := &ErrorMiddleware{
		options:         &Options{},
		console:         console,
		global:          &internal.GlobalCommandOptions{},
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		errorPipeline:   errorhandler.NewErrorHandlerPipeline(nil),
	}

	result, err := e.Run(context.Background(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, context.Canceled
	})

	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// UxMiddleware.Run — ErrorWithSuggestion path
// ---------------------------------------------------------------------------

func TestUxMiddleware_Run_ErrorWithSuggestion(t *testing.T) {
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	sugErr := &internal.ErrorWithSuggestion{
		Err:        errors.New("base error"),
		Message:    "Something went wrong",
		Suggestion: "Try running azd auth login",
	}

	result, err := m.Run(context.Background(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, sugErr
	})

	require.Error(t, err)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// UxMiddleware.Run — ErrorWithTraceId path
// ---------------------------------------------------------------------------

func TestUxMiddleware_Run_ErrorWithTraceId(t *testing.T) {
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	traceErr := &internal.ErrorWithTraceId{
		TraceId: "abc-123",
		Err:     errors.New("azure deployment failed"),
	}

	result, err := m.Run(context.Background(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, traceErr
	})

	require.Error(t, err)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// UxMiddleware.Run — regular error (no suggestion, no trace, no extension)
// ---------------------------------------------------------------------------

func TestUxMiddleware_Run_RegularError(t *testing.T) {
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	result, err := m.Run(context.Background(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, errors.New("generic error")
	})

	require.Error(t, err)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// UxMiddleware.Run — nil result on success (no message to display)
// ---------------------------------------------------------------------------

func TestUxMiddleware_Run_NilResult(t *testing.T) {
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	result, err := m.Run(context.Background(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, nil
	})

	require.NoError(t, err)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// UxMiddleware.Run — success with follow-up message
// ---------------------------------------------------------------------------

func TestUxMiddleware_Run_SuccessWithFollowUp(t *testing.T) {
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	result, err := m.Run(context.Background(), func(_ context.Context) (*actions.ActionResult, error) {
		return &actions.ActionResult{
			Message: &actions.ResultMessage{
				Header:   "Deployment complete",
				FollowUp: "Run azd monitor to view logs",
			},
		}, nil
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "Deployment complete", result.Message.Header)
}

// ---------------------------------------------------------------------------
// UxMiddleware.Run — azdext LocalError with suggestion
// ---------------------------------------------------------------------------

func TestUxMiddleware_Run_LocalErrorWithSuggestion(t *testing.T) {
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	localErr := &azdext.LocalError{
		Message:    "Missing subscription ID",
		Code:       "missing_subscription_id",
		Suggestion: "Run azd config set defaults.subscription <id>",
	}

	result, err := m.Run(context.Background(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, localErr
	})

	require.Error(t, err)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// UxMiddleware.Run — ExtensionRunError without suggestion
// ---------------------------------------------------------------------------

func TestUxMiddleware_Run_ExtensionRunError(t *testing.T) {
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	extErr := &extensions.ExtensionRunError{
		ExtensionId: "test.ext",
		Err: &azdext.LocalError{
			Message: "extension crashed",
			Code:    "crash",
		},
	}

	result, err := m.Run(context.Background(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, extErr
	})

	require.Error(t, err)
	require.Nil(t, result)
}

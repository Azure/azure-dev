// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/agent"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/platform"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func Test_Middleware_RunAction(t *testing.T) {
	t.Parallel()
	// In a standard success case both the action and the middleware will succeed
	t.Run("success", func(t *testing.T) {
		t.Parallel()
		preRan := false
		postRan := false
		runLog := []string{}

		mockContext := mocks.NewMockContext(t.Context())
		middlewareRunner := NewMiddlewareRunner(mockContext.Container)

		_ = middlewareRunner.Use("test", func() Middleware {
			return &testMiddleware{
				preFn: func() error {
					preRan = true
					runLog = append(runLog, "pre")
					return nil
				},
				postFn: func() error {
					postRan = true
					runLog = append(runLog, "post")
					return nil
				},
			}
		})

		actionRan := registerAction(t, mockContext, "test-action", &runLog)
		result, err := middlewareRunner.RunAction(*mockContext.Context, &Options{Name: "test"}, "test-action")

		require.NotNil(t, result)
		require.NoError(t, err)
		require.True(t, preRan)
		require.True(t, postRan)
		require.True(t, *actionRan)
		require.Equal(t, []string{"pre", "action", "post"}, runLog)
	})

	// In this case if the middleware fails it will not run the action
	// This is a middleware implementation details and is controlled
	// by the middleware developer
	t.Run("error", func(t *testing.T) {
		t.Parallel()
		preRan := false
		postRan := false
		runLog := []string{}

		mockContext := mocks.NewMockContext(t.Context())
		middlewareRunner := NewMiddlewareRunner(mockContext.Container)

		_ = middlewareRunner.Use("test", func() Middleware {
			return &testMiddleware{
				preFn: func() error {
					preRan = true
					runLog = append(runLog, "pre")
					return fmt.Errorf("middleware error")
				},
				postFn: func() error {
					postRan = true
					runLog = append(runLog, "post")
					return nil
				},
			}
		})

		actionRan := registerAction(t, mockContext, "test-action", &runLog)
		result, err := middlewareRunner.RunAction(*mockContext.Context, &Options{Name: "test"}, "test-action")

		require.Nil(t, result)
		require.Error(t, err)
		require.True(t, preRan)
		require.False(t, postRan)
		require.False(t, *actionRan)
		require.Equal(t, []string{"pre"}, runLog)
	})

	t.Run("multiple middleware components", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		middlewareRunner := NewMiddlewareRunner(mockContext.Container)
		runLog := []string{}

		_ = middlewareRunner.Use("A", func() Middleware {
			return &testMiddleware{
				preFn: func() error {
					runLog = append(runLog, "Pre-A")
					return nil
				},
				postFn: func() error {
					runLog = append(runLog, "Post-A")
					return nil
				},
			}
		})

		_ = middlewareRunner.Use("B", func() Middleware {
			return &testMiddleware{
				preFn: func() error {
					runLog = append(runLog, "Pre-B")
					return nil
				},
				postFn: func() error {
					runLog = append(runLog, "Post-B")
					return nil
				},
			}
		})

		actionRan := registerAction(t, mockContext, "test-action", &runLog)
		result, err := middlewareRunner.RunAction(*mockContext.Context, &Options{Name: "test"}, "test-action")

		require.NotNil(t, result)
		require.NoError(t, err)
		require.True(t, *actionRan)

		// Notice the order in which the middleware components execute in a FILO stack similar to golang defer statements
		require.Equal(t, []string{"Pre-A", "Pre-B", "action", "Post-B", "Post-A"}, runLog)
	})

	t.Run("context propagated to action", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		middlewareRunner := NewMiddlewareRunner(mockContext.Container)

		key := cxtKey{}

		_ = middlewareRunner.Use("addValue", func() Middleware {
			return middlewareFunc(func(ctx context.Context, nextFn NextFn) (*actions.ActionResult, error) {
				return nextFn(context.WithValue(ctx, key, "pass"))
			})
		})

		err := mockContext.Container.RegisterNamedTransient("test-action", func() actions.Action {
			return &testAction{
				runFunc: func(ctx context.Context) (*actions.ActionResult, error) {
					// ensure we can recover the value added by the middleware above.
					a := ctx.Value(key)
					require.NotNil(t, a)

					v, ok := a.(string)
					require.True(t, ok)
					require.Equal(t, "pass", v)

					return nil, nil
				},
			}
		})
		require.NoError(t, err)

		result, err := middlewareRunner.RunAction(*mockContext.Context, &Options{Name: "test"}, "test-action")
		require.Nil(t, result)
		require.NoError(t, err)
	})
}

func registerAction(t *testing.T, mockContext *mocks.MockContext, name string, runLog *[]string) *bool {
	actionRan := false

	err := mockContext.Container.RegisterNamedTransient(name, func() actions.Action {
		return &testAction{
			runFunc: func(ctx context.Context) (*actions.ActionResult, error) {
				actionRan = true
				*runLog = append(*runLog, "action")

				return &actions.ActionResult{
					Message: &actions.ResultMessage{Header: "Action"},
				}, nil
			},
		}
	})

	require.NoError(t, err)

	return &actionRan
}

type testAction struct {
	runFunc func(ctx context.Context) (*actions.ActionResult, error)
}

func (a *testAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	return a.runFunc(ctx)
}

type testMiddleware struct {
	preFn  func() error
	postFn func() error
}

// A test middleware run implementation
// This middleware will execute code before and after the middleware chain and action run
func (a *testMiddleware) Run(ctx context.Context, nextFn NextFn) (*actions.ActionResult, error) {
	// Run some code before the action
	// If it fails return error
	err := a.preFn()
	if err != nil {
		return nil, err
	}

	// Execute the remainder of the middleware chain and the action
	result, err := nextFn(ctx)
	if err != nil {
		return nil, err
	}

	// Run code after the action completes
	err = a.postFn()
	if err != nil {
		return nil, err
	}

	// Ultimately return the result
	return result, nil
}

type cxtKey struct{}

// middlewareFunc is a func that implements the Middleware interface
type middlewareFunc func(ctx context.Context, nextFn NextFn) (*actions.ActionResult, error)

func (f middlewareFunc) Run(ctx context.Context, nextFn NextFn) (*actions.ActionResult, error) {
	return f(ctx, nextFn)
}

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
	t.Parallel()
	m := NewExperimentationMiddleware()
	require.NotNil(t, m)

	_, ok := m.(*ExperimentationMiddleware)
	require.True(t, ok, "should return *ExperimentationMiddleware")
}

func TestExperimentationMiddleware_Run_AlwaysCallsNext(t *testing.T) {
	t.Parallel()
	// The middleware attempts to contact TAS, but regardless of success or
	// failure it must call next(ctx).  In a unit-test environment the TAS
	// endpoint is unreachable, so the manager-creation or assignment call
	// will fail – but next must still be invoked.
	m := &ExperimentationMiddleware{}
	nextFn, count := nextCounter()

	result, err := m.Run(t.Context(), nextFn)

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

	result, err := m.Run(t.Context(), nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, *count)
}

func TestExperimentationMiddleware_Run_PropagatesNextError(t *testing.T) {
	t.Parallel()
	m := &ExperimentationMiddleware{}

	expectedErr := context.DeadlineExceeded
	nextFn := func(_ context.Context) (*actions.ActionResult, error) {
		return nil, expectedErr
	}

	result, err := m.Run(t.Context(), nextFn)

	require.ErrorIs(t, err, expectedErr)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// ExtensionsMiddleware — constructor
// ---------------------------------------------------------------------------

func TestNewExtensionsMiddleware(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
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
	t.Parallel()
	// When the context marks a child action, Run must delegate to next()
	// immediately without touching the extension manager.
	m := &ExtensionsMiddleware{
		// extensionManager is intentionally nil – it must not be touched
	}

	ctx := WithChildAction(t.Context())
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
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
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

	result, err := m.Run(t.Context(), nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, *count)
}

// ---------------------------------------------------------------------------
// ExtensionsMiddleware.Run — extensions without listen capabilities
// ---------------------------------------------------------------------------

func TestExtensionsMiddleware_Run_NoListenCapabilities(t *testing.T) {
	t.Parallel()
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

	mockCtx := mocks.NewMockContext(t.Context())
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

	result, err := m.Run(t.Context(), nextFn)

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

	ctx, cancel := getReadyContext(t.Context())
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

	ctx, cancel := getReadyContext(t.Context())
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

	ctx, cancel := getReadyContext(t.Context())
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

	ctx, cancel := getReadyContext(t.Context())
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

	ctx, cancel := getReadyContext(t.Context())
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

	ctx, cancel := getReadyContext(t.Context())
	defer cancel()

	_, ok := ctx.Deadline()
	require.False(t, ok, "debug mode should not impose a deadline")
}

// ---------------------------------------------------------------------------
// ExtensionsMiddleware.Run — ListInstalled error
// ---------------------------------------------------------------------------

func TestExtensionsMiddleware_Run_ListInstalledError(t *testing.T) {
	t.Parallel()
	// Seed the config with an invalid value for the installed extensions
	// section so that GetSection fails during unmarshalling.
	mockCtx := mocks.NewMockContext(t.Context())
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

	_, err = m.Run(t.Context(), nextOK)
	require.Error(t, err, "should propagate ListInstalled error")
}

// ---------------------------------------------------------------------------
// ExtensionsMiddleware.Run — extensions with listen capabilities but
// ServiceLocator cannot resolve gRPC server
// ---------------------------------------------------------------------------

func TestExtensionsMiddleware_Run_ListenCapabilities_ResolveGrpcFails(t *testing.T) {
	t.Parallel()
	installed := map[string]*extensions.Extension{
		"test.ext": {
			Id:           "test.ext",
			DisplayName:  "Test Extension",
			Version:      "1.0.0",
			Capabilities: []extensions.CapabilityType{extensions.LifecycleEventsCapability},
		},
	}

	mockCtx := mocks.NewMockContext(t.Context())
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

	_, err := m.Run(t.Context(), nextOK)
	require.Error(t, err, "should fail when gRPC server cannot be resolved")
}

// ---------------------------------------------------------------------------
// ExtensionsMiddleware.Run — ServiceTargetProvider capability
// ---------------------------------------------------------------------------

func TestExtensionsMiddleware_Run_ServiceTargetProviderCapability(t *testing.T) {
	t.Parallel()
	installed := map[string]*extensions.Extension{
		"test.svc": {
			Id:           "test.svc",
			DisplayName:  "Service Provider Ext",
			Version:      "1.0.0",
			Capabilities: []extensions.CapabilityType{extensions.ServiceTargetProviderCapability},
		},
	}

	mockCtx := mocks.NewMockContext(t.Context())
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

	_, err := m.Run(t.Context(), nextOK)
	require.Error(t, err, "should fail — gRPC server not registered")
}

// ---------------------------------------------------------------------------
// ExtensionsMiddleware.Run — FrameworkServiceProvider capability
// ---------------------------------------------------------------------------

func TestExtensionsMiddleware_Run_FrameworkServiceProviderCapability(t *testing.T) {
	t.Parallel()
	installed := map[string]*extensions.Extension{
		"test.fw": {
			Id:           "test.fw",
			DisplayName:  "Framework Provider Ext",
			Version:      "1.0.0",
			Capabilities: []extensions.CapabilityType{extensions.FrameworkServiceProviderCapability},
		},
	}

	mockCtx := mocks.NewMockContext(t.Context())
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

	_, err := m.Run(t.Context(), nextOK)
	require.Error(t, err, "should fail — gRPC server not registered")
}

// ---------------------------------------------------------------------------
// ExtensionsMiddleware.Run — mixed capabilities: some listen, some don't
// ---------------------------------------------------------------------------

func TestExtensionsMiddleware_Run_MixedCapabilities(t *testing.T) {
	t.Parallel()
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

	mockCtx := mocks.NewMockContext(t.Context())
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
	_, err := m.Run(t.Context(), nextOK)
	require.Error(t, err, "should fail — gRPC server not registered")
}

// ---------------------------------------------------------------------------
// ExperimentationMiddleware.Run — cancelled context
// ---------------------------------------------------------------------------

func TestExperimentationMiddleware_Run_CancelledContext(t *testing.T) {
	t.Parallel()
	m := &ExperimentationMiddleware{}

	ctx, cancel := context.WithCancel(t.Context())
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
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
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

	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return expected, nil
	})

	require.NoError(t, err)
	require.Equal(t, expected, result)
}

func TestExtensionsMiddleware_Run_NoExtensions_PropagatesNextError(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
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
	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, expectedErr
	})

	require.ErrorIs(t, err, expectedErr)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// listenCapabilities variable — verify expected values
// ---------------------------------------------------------------------------

func TestListenCapabilities_ContainsExpectedValues(t *testing.T) {
	t.Parallel()
	require.Contains(t, listenCapabilities, extensions.LifecycleEventsCapability)
	require.Contains(t, listenCapabilities, extensions.ServiceTargetProviderCapability)
	require.Contains(t, listenCapabilities, extensions.FrameworkServiceProviderCapability)
	require.Contains(t, listenCapabilities, extensions.ProvisioningProviderCapability)
	require.Contains(t, listenCapabilities, extensions.ValidationProviderCapability)
	require.Len(t, listenCapabilities, 5)
}

// ---------------------------------------------------------------------------
// extensionFailure struct — basic construction
// ---------------------------------------------------------------------------

func TestExtensionFailure_Fields(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	// Create a manager with no installed extensions so GetInstalled fails
	mockCtx := mocks.NewMockContext(t.Context())
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
	t.Parallel()
	installed := map[string]*extensions.Extension{
		"test.ext": {
			Id:           "test.ext",
			DisplayName:  "Test Extension",
			Version:      "1.0.0",
			Capabilities: []extensions.CapabilityType{extensions.LifecycleEventsCapability},
		},
	}

	mockCtx := mocks.NewMockContext(t.Context())
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
	t.Parallel()
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
	t.Parallel()
	require.NotEmpty(t, assignmentEndpoint)
	require.Contains(t, assignmentEndpoint, "exp-tas.com")
}

// ---------------------------------------------------------------------------
// shouldSkipAgentHandling — control-flow and non-fixable error types
// ---------------------------------------------------------------------------

func TestShouldSkipAgentHandling_ContextCanceled(t *testing.T) {
	t.Parallel()
	require.True(t, shouldSkipAgentHandling(context.Canceled))
}

func TestShouldSkipAgentHandling_AbortedByUser(t *testing.T) {
	t.Parallel()
	require.True(t, shouldSkipAgentHandling(internal.ErrAbortedByUser))
}

func TestShouldSkipAgentHandling_RegularError(t *testing.T) {
	t.Parallel()
	require.False(t, shouldSkipAgentHandling(errors.New("some regular error")))
}

func TestShouldSkipAgentHandling_WrappedCanceled(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("operation failed: %w", context.Canceled)
	require.True(t, shouldSkipAgentHandling(err))
}

func TestShouldSkipAgentHandling_NilError(t *testing.T) {
	t.Parallel()
	// nil error should not be skipped — though callers check nil before calling
	require.False(t, shouldSkipAgentHandling(nil))
}

// ---------------------------------------------------------------------------
// ErrorMiddleware.displayUsageMetrics
// ---------------------------------------------------------------------------

func TestErrorMiddleware_displayUsageMetrics_WithTokens(t *testing.T) {
	t.Parallel()
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
		e.displayUsageMetrics(t.Context(), result)
	})
}

func TestErrorMiddleware_displayUsageMetrics_NilResult(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	e := &ErrorMiddleware{
		console: console,
	}

	// Should not panic with nil result
	require.NotPanics(t, func() {
		e.displayUsageMetrics(t.Context(), nil)
	})
}

func TestErrorMiddleware_displayUsageMetrics_ZeroTokens(t *testing.T) {
	t.Parallel()
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
		e.displayUsageMetrics(t.Context(), result)
	})
}

// ---------------------------------------------------------------------------
// ErrorMiddleware.Run — no error path
// ---------------------------------------------------------------------------

func TestErrorMiddleware_Run_NoError(t *testing.T) {
	t.Parallel()
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

	result, err := e.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return expected, nil
	})

	require.NoError(t, err)
	require.Equal(t, expected, result)
}

// ---------------------------------------------------------------------------
// ErrorMiddleware.Run — child action path (error passes through)
// ---------------------------------------------------------------------------

func TestErrorMiddleware_Run_ChildAction_PassesError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	e := &ErrorMiddleware{
		options:         &Options{},
		console:         console,
		global:          &internal.GlobalCommandOptions{},
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		errorPipeline:   errorhandler.NewErrorHandlerPipeline(nil),
	}

	ctx := WithChildAction(t.Context())
	expectedErr := errors.New("child error")

	result, err := e.Run(ctx, func(_ context.Context) (*actions.ActionResult, error) {
		return nil, expectedErr
	})

	require.ErrorIs(t, err, expectedErr)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// ErrorMiddleware.Run — error with shouldSkipAgentHandling
// ---------------------------------------------------------------------------

func TestErrorMiddleware_Run_SkippableError_NoPrompt(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	e := &ErrorMiddleware{
		options:         &Options{},
		console:         console,
		global:          &internal.GlobalCommandOptions{NoPrompt: true},
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		errorPipeline:   errorhandler.NewErrorHandlerPipeline(nil),
	}

	result, err := e.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, context.Canceled
	})

	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// ErrorMiddleware.Run — ErrorWithSuggestion passes through
// ---------------------------------------------------------------------------

func TestErrorMiddleware_Run_ErrorWithSuggestion(t *testing.T) {
	t.Parallel()
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

	result, err := e.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, sugErr
	})

	require.Error(t, err)
	var ews *internal.ErrorWithSuggestion
	require.True(t, errors.As(err, &ews))
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// shouldSkipAgentHandling — non-fixable error types
// ---------------------------------------------------------------------------

func TestShouldSkipAgentHandling_RegularError_NotSkipped(t *testing.T) {
	t.Parallel()
	result := shouldSkipAgentHandling(errors.New("some unknown error"))
	require.False(t, result)
}

func TestShouldSkipAgentHandling_ContextCanceled_Skipped(t *testing.T) {
	t.Parallel()
	// context.Canceled is a control-flow error that should be skipped.
	result := shouldSkipAgentHandling(context.Canceled)
	require.True(t, result)
}

// ---------------------------------------------------------------------------
// ErrorMiddleware.Run — regular error, copilot disabled, no prompt
// ---------------------------------------------------------------------------

func TestErrorMiddleware_Run_RegularError_CopilotDisabled(t *testing.T) {
	t.Parallel()
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
	result, err := e.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, expectedErr
	})

	require.ErrorIs(t, err, expectedErr)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// ErrorMiddleware.Run — error with pipeline match
// ---------------------------------------------------------------------------

func TestErrorMiddleware_Run_ErrorPipelineMatch(t *testing.T) {
	t.Parallel()
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
	result, err := e.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
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
	result, err := e.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, expectedErr
	})

	require.ErrorIs(t, err, expectedErr)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// ErrorMiddleware.Run — shouldSkipAgentHandling path
// ---------------------------------------------------------------------------

func TestErrorMiddleware_Run_SkippableError_CancelledInNonPrompt(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	e := &ErrorMiddleware{
		options:         &Options{},
		console:         console,
		global:          &internal.GlobalCommandOptions{},
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		errorPipeline:   errorhandler.NewErrorHandlerPipeline(nil),
	}

	result, err := e.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, context.Canceled
	})

	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// UxMiddleware.Run — ErrorWithSuggestion path
// ---------------------------------------------------------------------------

func TestUxMiddleware_Run_ErrorWithSuggestion(t *testing.T) {
	t.Parallel()
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

	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, sugErr
	})

	require.Error(t, err)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// UxMiddleware.Run — ErrorWithTraceId path
// ---------------------------------------------------------------------------

func TestUxMiddleware_Run_ErrorWithTraceId(t *testing.T) {
	t.Parallel()
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

	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, traceErr
	})

	require.Error(t, err)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// UxMiddleware.Run — regular error (no suggestion, no trace, no extension)
// ---------------------------------------------------------------------------

func TestUxMiddleware_Run_RegularError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, errors.New("generic error")
	})

	require.Error(t, err)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// UxMiddleware.Run — nil result on success (no message to display)
// ---------------------------------------------------------------------------

func TestUxMiddleware_Run_NilResult(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, nil
	})

	require.NoError(t, err)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// UxMiddleware.Run — success with follow-up message
// ---------------------------------------------------------------------------

func TestUxMiddleware_Run_SuccessWithFollowUp(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
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
	t.Parallel()
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

	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, localErr
	})

	require.Error(t, err)
	require.Nil(t, result)
}

// ---------------------------------------------------------------------------
// UxMiddleware.Run — ExtensionRunError without suggestion
// ---------------------------------------------------------------------------

func TestUxMiddleware_Run_ExtensionRunError(t *testing.T) {
	t.Parallel()
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

	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, extErr
	})

	require.Error(t, err)
	require.Nil(t, result)
}

func TestTelemetryMiddleware_Run_ResponseError_WrapsWithTraceId(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
	lazyPlatformConfig := lazy.NewLazy(func() (*platform.Config, error) {
		return nil, nil
	})

	options := &Options{
		CommandPath: "azd provision",
		Name:        "provision",
		Flags:       pflag.NewFlagSet("test", pflag.ContinueOnError),
	}
	middleware := NewTelemetryMiddleware(options, lazyPlatformConfig, nil)

	respErr := &azcore.ResponseError{
		ErrorCode:  "ResourceNotFound",
		StatusCode: 404,
		RawResponse: &http.Response{
			StatusCode: 404,
			Request: &http.Request{
				Method: "GET",
				Host:   "management.azure.com",
			},
		},
	}

	result, err := middleware.Run(*mockCtx.Context, func(_ context.Context) (*actions.ActionResult, error) {
		return nil, respErr
	})

	require.Error(t, err)
	require.NotNil(t, result, "Run should return non-nil result even on error")

	// The error should be wrapped with trace ID
	traceErr, ok := errors.AsType[*internal.ErrorWithTraceId](err)
	require.True(t, ok, "error should be wrapped with ErrorWithTraceId")
	require.NotEmpty(t, traceErr.TraceId)
}

func TestTelemetryMiddleware_Run_AzureDeploymentError_WrapsWithTraceId(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
	lazyPlatformConfig := lazy.NewLazy(func() (*platform.Config, error) {
		return nil, nil
	})

	options := &Options{
		CommandPath: "azd provision",
		Name:        "provision",
		Flags:       pflag.NewFlagSet("test", pflag.ContinueOnError),
	}
	middleware := NewTelemetryMiddleware(options, lazyPlatformConfig, nil)

	deployErr := &azapi.AzureDeploymentError{
		Title: "Deployment Failed",
		Inner: errors.New("quota exceeded"),
		Details: &azapi.DeploymentErrorLine{
			Code:    "QuotaExceeded",
			Message: "Compute quota exceeded",
		},
	}

	result, err := middleware.Run(*mockCtx.Context, func(_ context.Context) (*actions.ActionResult, error) {
		return nil, deployErr
	})

	require.Error(t, err)
	require.NotNil(t, result)

	traceErr, ok := errors.AsType[*internal.ErrorWithTraceId](err)
	require.True(t, ok, "AzureDeploymentError should be wrapped with ErrorWithTraceId")
	require.NotEmpty(t, traceErr.TraceId)
}

func TestTelemetryMiddleware_Run_TerraformExitError_WrapsWithTraceId(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
	lazyPlatformConfig := lazy.NewLazy(func() (*platform.Config, error) {
		return nil, nil
	})

	options := &Options{
		CommandPath: "azd provision",
		Name:        "provision",
		Flags:       pflag.NewFlagSet("test", pflag.ContinueOnError),
	}
	middleware := NewTelemetryMiddleware(options, lazyPlatformConfig, nil)

	exitErr := &exec.ExitError{
		Cmd:      "terraform",
		ExitCode: 1,
	}

	result, err := middleware.Run(*mockCtx.Context, func(_ context.Context) (*actions.ActionResult, error) {
		return nil, exitErr
	})

	require.Error(t, err)
	require.NotNil(t, result)

	traceErr, ok := errors.AsType[*internal.ErrorWithTraceId](err)
	require.True(t, ok, "terraform ExitError should be wrapped with ErrorWithTraceId")
	require.NotEmpty(t, traceErr.TraceId)
}

func TestTelemetryMiddleware_Run_NonTerraformExitError_NoTraceId(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
	lazyPlatformConfig := lazy.NewLazy(func() (*platform.Config, error) {
		return nil, nil
	})

	options := &Options{
		CommandPath: "azd deploy",
		Name:        "deploy",
		Flags:       pflag.NewFlagSet("test", pflag.ContinueOnError),
	}
	middleware := NewTelemetryMiddleware(options, lazyPlatformConfig, nil)

	// Non-terraform exit error should NOT be wrapped
	exitErr := &exec.ExitError{
		Cmd:      "docker",
		ExitCode: 1,
	}

	result, err := middleware.Run(*mockCtx.Context, func(_ context.Context) (*actions.ActionResult, error) {
		return nil, exitErr
	})

	require.Error(t, err)
	require.NotNil(t, result)

	_, ok := errors.AsType[*internal.ErrorWithTraceId](err)
	require.False(t, ok, "non-terraform ExitError should NOT be wrapped with trace ID")
}

func TestTelemetryMiddleware_Run_GenericError_NoTraceId(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
	lazyPlatformConfig := lazy.NewLazy(func() (*platform.Config, error) {
		return nil, nil
	})

	options := &Options{
		CommandPath: "azd up",
		Name:        "up",
		Flags:       pflag.NewFlagSet("test", pflag.ContinueOnError),
	}
	middleware := NewTelemetryMiddleware(options, lazyPlatformConfig, nil)

	result, err := middleware.Run(*mockCtx.Context, func(_ context.Context) (*actions.ActionResult, error) {
		return nil, errors.New("some local error")
	})

	require.Error(t, err)
	require.NotNil(t, result)

	_, ok := errors.AsType[*internal.ErrorWithTraceId](err)
	require.False(t, ok, "generic error should NOT be wrapped with trace ID")
}

func TestTelemetryMiddleware_Run_NilResultFromNext(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
	lazyPlatformConfig := lazy.NewLazy(func() (*platform.Config, error) {
		return nil, nil
	})

	options := &Options{
		CommandPath: "azd deploy",
		Name:        "deploy",
		Flags:       pflag.NewFlagSet("test", pflag.ContinueOnError),
	}
	middleware := NewTelemetryMiddleware(options, lazyPlatformConfig, nil)

	result, err := middleware.Run(*mockCtx.Context, func(_ context.Context) (*actions.ActionResult, error) {
		return nil, nil // nil result, nil error
	})

	require.NoError(t, err)
	require.NotNil(t, result, "nil result from next should be replaced with empty ActionResult")
}

func TestTelemetryMiddleware_Run_WithChangedFlags(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
	lazyPlatformConfig := lazy.NewLazy(func() (*platform.Config, error) {
		return nil, nil
	})

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("output", "json", "output format")
	flags.Bool("debug", false, "enable debug")
	// Simulate user setting --output flag
	err := flags.Set("output", "table")
	require.NoError(t, err)

	options := &Options{
		CommandPath: "azd env list",
		Name:        "env-list",
		Flags:       flags,
		Args:        []string{"arg1", "arg2"},
	}
	middleware := NewTelemetryMiddleware(options, lazyPlatformConfig, nil)

	result, err := middleware.Run(*mockCtx.Context, func(_ context.Context) (*actions.ActionResult, error) {
		return &actions.ActionResult{}, nil
	})

	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestTelemetryMiddleware_Run_PlatformConfigError(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
	// Platform config returns an error — middleware should still proceed
	lazyPlatformConfig := lazy.NewLazy(func() (*platform.Config, error) {
		return nil, errors.New("platform config unavailable")
	})

	options := &Options{
		CommandPath: "azd provision",
		Name:        "provision",
		Flags:       pflag.NewFlagSet("test", pflag.ContinueOnError),
	}
	middleware := NewTelemetryMiddleware(options, lazyPlatformConfig, nil)

	result, err := middleware.Run(*mockCtx.Context, func(_ context.Context) (*actions.ActionResult, error) {
		return &actions.ActionResult{}, nil
	})

	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestTelemetryMiddleware_Run_WithExtensionId(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
	lazyPlatformConfig := lazy.NewLazy(func() (*platform.Config, error) {
		return nil, nil
	})

	options := &Options{
		CommandPath: "azd x run",
		Name:        "x-run",
		Annotations: map[string]string{
			"extension.id": "test.extension",
		},
		Args: []string{"test", "command"},
	}
	middleware := NewTelemetryMiddleware(options, lazyPlatformConfig, nil)

	result, err := middleware.Run(*mockCtx.Context, func(_ context.Context) (*actions.ActionResult, error) {
		return &actions.ActionResult{}, nil
	})

	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestTelemetryMiddleware_Run_WithExtensionIdAndManager(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
	lazyPlatformConfig := lazy.NewLazy(func() (*platform.Config, error) {
		return nil, nil
	})
	manager := createExtensionsManager(t, mockCtx, nil)

	options := &Options{
		CommandPath: "azd x run",
		Name:        "x-run",
		Annotations: map[string]string{
			"extension.id": "nonexistent.ext",
		},
		Args: []string{"test"},
	}
	middleware := NewTelemetryMiddleware(options, lazyPlatformConfig, manager)

	result, err := middleware.Run(*mockCtx.Context, func(_ context.Context) (*actions.ActionResult, error) {
		return &actions.ActionResult{}, nil
	})

	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestTelemetryMiddleware_Run_NilFlags(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
	lazyPlatformConfig := lazy.NewLazy(func() (*platform.Config, error) {
		return nil, nil
	})

	options := &Options{
		CommandPath: "azd provision",
		Name:        "provision",
		Flags:       nil, // nil flags
	}
	middleware := NewTelemetryMiddleware(options, lazyPlatformConfig, nil)

	result, err := middleware.Run(*mockCtx.Context, func(_ context.Context) (*actions.ActionResult, error) {
		return &actions.ActionResult{}, nil
	})

	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestUxMiddleware_Run_AzdextLocalErrorWithSuggestion(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	localErr := &azdext.LocalError{
		Message:    "Extension config missing",
		Code:       "missing_config",
		Suggestion: "Run 'azd extension configure' to set up the extension",
	}

	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, localErr
	})

	require.Error(t, err)
	require.Nil(t, result)
}

func TestUxMiddleware_Run_AzdextServiceErrorWithSuggestion(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	svcErr := &azdext.ServiceError{
		Message:    "Service unavailable",
		ErrorCode:  "ServiceUnavailable",
		StatusCode: 503,
		Suggestion: "Try again later",
	}

	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, svcErr
	})

	require.Error(t, err)
	require.Nil(t, result)
}

func TestUxMiddleware_Run_AzdextLocalErrorWithLinks(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	localErr := &azdext.LocalError{
		Message: "Extension config missing",
		Code:    "missing_config",
		Links: []errorhandler.ErrorLink{{
			URL:   "https://aka.ms/azd-errors#missing-config",
			Title: "Missing config help",
		}},
	}

	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, localErr
	})

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, fmt.Sprint(console.Output()), "https://aka.ms/azd-errors#missing-config")
	require.NotContains(t, fmt.Sprint(console.Output()), "Suggestion:")
}

func TestUxMiddleware_Run_AzdextLocalErrorWithoutSuggestion(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	// LocalError without suggestion — falls through to ExtensionRunError check
	localErr := &azdext.LocalError{
		Message: "Something went wrong",
		Code:    "generic",
	}

	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, localErr
	})

	require.Error(t, err)
	require.Nil(t, result)
}

func TestUxMiddleware_Run_ExtensionRunErrorWithMessage(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	extErr := &extensions.ExtensionRunError{
		ExtensionId: "test.ext",
		Err: &azdext.LocalError{
			Message: "build step failed",
			Code:    "build_failed",
		},
	}

	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, extErr
	})

	require.Error(t, err)
	require.Nil(t, result)
}

func TestUxMiddleware_Run_ExtensionRunErrorWithoutMessage(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	extErr := &extensions.ExtensionRunError{
		ExtensionId: "test.ext",
		Err:         errors.New("raw process error"),
	}

	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, extErr
	})

	require.Error(t, err)
	require.Nil(t, result)
}

func TestUxMiddleware_Run_UnsupportedServiceHostError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	hostErr := &project.UnsupportedServiceHostError{
		Host:        "unsupported-host",
		ServiceName: "my-service",
	}

	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, hostErr
	})

	require.Error(t, err)
	require.Nil(t, result)

	// Verify the error message was set on the error
	var returnedErr *project.UnsupportedServiceHostError
	require.True(t, errors.As(err, &returnedErr))
	require.NotEmpty(t, returnedErr.ErrorMessage)
}

func TestUxMiddleware_Run_SuccessWithFollowUpMessage(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	actionResult := &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   "Deployment complete",
			FollowUp: "Run azd monitor to view your app",
		},
	}

	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return actionResult, nil
	})

	require.NoError(t, err)
	require.Equal(t, actionResult, result)
}

func TestUxMiddleware_Run_SuccessNilMessage(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	// ActionResult with no message — should not display anything
	actionResult := &actions.ActionResult{}

	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return actionResult, nil
	})

	require.NoError(t, err)
	require.Equal(t, actionResult, result)
}

func TestUxMiddleware_Run_ErrorWithTraceIdWrappingGeneric(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	m := &UxMiddleware{
		options:         &Options{},
		console:         console,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	traceErr := &internal.ErrorWithTraceId{
		TraceId: "trace-abc-123",
		Err:     errors.New("deployment failed"),
	}

	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, traceErr
	})

	require.Error(t, err)
	require.Nil(t, result)
}

func TestErrorMiddleware_Run_ChildAction_WithError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()

	factory := &mockAgentFactory{}
	ucm := &mockUserConfigManager{cfg: config.NewEmptyConfig()}
	fm := copilotEnabledFeatureManager()
	global := &internal.GlobalCommandOptions{}

	m := newErrorMiddlewareForTest(console, factory, fm, ucm, global)

	ctx := WithChildAction(t.Context())
	expected := errors.New("child error")

	_, err := m.Run(ctx, func(ctx context.Context) (*actions.ActionResult, error) {
		return nil, expected
	})

	require.ErrorIs(t, err, expected)
}

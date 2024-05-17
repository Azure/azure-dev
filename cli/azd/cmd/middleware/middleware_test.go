package middleware

import (
	"context"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_Middleware_RunAction(t *testing.T) {
	// In a standard success case both the action and the middleware will succeed
	t.Run("success", func(t *testing.T) {
		preRan := false
		postRan := false
		runLog := []string{}

		mockContext := mocks.NewMockContext(context.Background())
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
		preRan := false
		postRan := false
		runLog := []string{}

		mockContext := mocks.NewMockContext(context.Background())
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
		mockContext := mocks.NewMockContext(context.Background())
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
		mockContext := mocks.NewMockContext(context.Background())
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

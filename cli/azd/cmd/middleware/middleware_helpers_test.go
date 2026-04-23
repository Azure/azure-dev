// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestWithChildAction_IsChildAction(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ctx  context.Context
		want bool
	}{
		{
			name: "PlainContext",
			ctx:  context.Background(),
			want: false,
		},
		{
			name: "ChildActionContext",
			ctx:  WithChildAction(context.Background()),
			want: true,
		},
		{
			name: "NestedChildActionContext",
			ctx: WithChildAction(
				WithChildAction(context.Background()),
			),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, IsChildAction(tt.ctx))
		})
	}
}

func TestIsChildAction_WrongType(t *testing.T) {
	t.Parallel()
	// Manually set a non-bool value under the same key
	ctx := context.WithValue(
		context.Background(),
		childActionKey,
		"not-a-bool",
	)
	require.False(t, IsChildAction(ctx))
}

func TestIsChildAction_FalseValue(t *testing.T) {
	t.Parallel()
	ctx := context.WithValue(
		context.Background(),
		childActionKey,
		false,
	)
	require.False(t, IsChildAction(ctx))
}

func TestOptions_WithContainer(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	opts := &Options{
		Name: "test",
	}
	require.Nil(t, opts.container)

	opts.WithContainer(mockContext.Container)
	require.NotNil(t, opts.container)
	require.Equal(t, mockContext.Container, opts.container)
}

func TestMiddlewareRunner_Use_AddsSingleEntry(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())
	runner := NewMiddlewareRunner(mockContext.Container)

	err := runner.Use("single", func() Middleware {
		return middlewareFunc(
			func(
				ctx context.Context,
				next NextFn,
			) (*actions.ActionResult, error) {
				return next(ctx)
			},
		)
	})
	require.NoError(t, err)
	require.Len(t, runner.chain, 1)
	require.Equal(t, "single", runner.chain[0])
}

func TestMiddlewareRunner_Use_MultipleMiddleware(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())
	runner := NewMiddlewareRunner(mockContext.Container)

	for _, name := range []string{"first", "second", "third"} {
		err := runner.Use(name, func() Middleware {
			return middlewareFunc(
				func(
					ctx context.Context,
					next NextFn,
				) (*actions.ActionResult, error) {
					return next(ctx)
				},
			)
		})
		require.NoError(t, err)
	}

	require.Len(t, runner.chain, 3)
	require.Equal(t,
		[]string{"first", "second", "third"},
		runner.chain,
	)
}

func TestMiddlewareRunner_RunAction_WithOptionsContainer(t *testing.T) {
	t.Parallel()
	// Verify that Options.container is used when set
	mockContext := mocks.NewMockContext(context.Background())
	runner := NewMiddlewareRunner(mockContext.Container)

	actionRan := false
	err := mockContext.Container.RegisterNamedTransient(
		"test-action", func() actions.Action {
			return &testAction{
				runFunc: func(
					ctx context.Context,
				) (*actions.ActionResult, error) {
					actionRan = true
					return &actions.ActionResult{
						Message: &actions.ResultMessage{
							Header: "OK",
						},
					}, nil
				},
			}
		})
	require.NoError(t, err)

	opts := &Options{
		Name:      "test",
		container: mockContext.Container,
	}

	result, err := runner.RunAction(
		*mockContext.Context, opts, "test-action")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, actionRan)
}

func TestMiddlewareRunner_RunAction_NoMiddleware(t *testing.T) {
	t.Parallel()
	// When no middleware is registered, the action runs directly
	mockContext := mocks.NewMockContext(context.Background())
	runner := NewMiddlewareRunner(mockContext.Container)

	err := mockContext.Container.RegisterNamedTransient(
		"direct-action", func() actions.Action {
			return &testAction{
				runFunc: func(
					ctx context.Context,
				) (*actions.ActionResult, error) {
					return &actions.ActionResult{
						Message: &actions.ResultMessage{
							Header: "Direct",
						},
					}, nil
				},
			}
		})
	require.NoError(t, err)

	result, err := runner.RunAction(
		*mockContext.Context,
		&Options{Name: "test"},
		"direct-action",
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "Direct", result.Message.Header)
}

func TestMiddlewareRunner_RunAction_InvalidAction(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())
	runner := NewMiddlewareRunner(mockContext.Container)

	// Don't register any action — resolution should fail
	_, err := runner.RunAction(
		*mockContext.Context,
		&Options{Name: "test"},
		"nonexistent-action",
	)
	require.Error(t, err)
}

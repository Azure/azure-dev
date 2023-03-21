package middleware

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_Telemetry_Run(t *testing.T) {
	t.Run("WithRootAction", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		options := &Options{
			CommandPath:   "azd infra create",
			Name:          "create",
			isChildAction: false,
		}
		middleware := NewTelemetryMiddleware(options)

		ran := false
		var actualContext context.Context

		nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
			ran = true
			actualContext = ctx
			return nil, nil
		}

		_, _ = middleware.Run(*mockContext.Context, nextFn)

		require.True(t, ran)
		require.NotEqual(
			t,
			*mockContext.Context,
			actualContext,
			"Context should be a different instance since telemetry creates a new context",
		)
	})

	t.Run("WithChildAction", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		options := &Options{
			CommandPath:   "azd infra create",
			Name:          "create",
			isChildAction: true,
		}
		middleware := NewTelemetryMiddleware(options)

		ran := false
		var actualContext context.Context

		nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
			ran = true
			actualContext = ctx
			return nil, nil
		}

		_, _ = middleware.Run(*mockContext.Context, nextFn)

		require.True(t, ran)
		require.Equal(
			t,
			*mockContext.Context,
			actualContext,
			"Context should be the same instance since it ignores child actions",
		)
	})
}

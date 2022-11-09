package middleware

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/events"
	"go.opentelemetry.io/otel/codes"
)

func UseTelemetry() actions.MiddlewareFn {
	return func(ctx context.Context, options *actions.ActionOptions, next actions.NextFn) (*actions.ActionResult, error) {
		if options != nil && options.DisableTelemetry {
			return next(ctx)
		}

		// Note: CommandPath is constructed using the Use member on each command up to the root.
		// It does not contain user input, and is safe for telemetry emission.
		spanCtx, span := telemetry.GetTracer().Start(ctx, events.GetCommandEventName(options.Name))
		defer span.End()

		result, err := next(spanCtx)
		if err != nil {
			span.SetStatus(codes.Error, "UnknownError")
		}

		return result, err
	}
}

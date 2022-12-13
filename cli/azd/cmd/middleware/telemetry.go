package middleware

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/events"
	"go.opentelemetry.io/otel/codes"
)

type TelemetryMiddleware struct {
	opt *actions.BuildOptions
}

func NewTelemetryMiddleware(opt *actions.BuildOptions) *TelemetryMiddleware {
	return &TelemetryMiddleware{
		opt,
	}
}

func (m *TelemetryMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	if m.opt.DisableTelemetry {
		return next(ctx)
	}

	// Note: CommandPath is constructed using the Use member on each command up to the root.
	// It does not contain user input, and is safe for telemetry emission.
	spanCtx, span := telemetry.GetTracer().Start(ctx, events.GetCommandEventName(m.opt.CommandName))
	defer span.End()

	result, err := next(spanCtx)
	if err != nil {
		span.SetStatus(codes.Error, "UnknownError")
	}

	return result, err
}

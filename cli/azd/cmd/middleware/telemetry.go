package middleware

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/events"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/fields"
	"github.com/spf13/pflag"

	"go.opentelemetry.io/otel/codes"
)

// Telemetry middleware tracks telemetry for the given action
type TelemetryMiddleware struct {
	options *Options
}

// Creates a new Telemetry middleware instance
func NewTelemetryMiddleware(options *Options) Middleware {
	return &TelemetryMiddleware{
		options: options,
	}
}

// Invokes the middleware and wraps the action with a telemetry span for telemetry reporting
func (m *TelemetryMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	// If the executing action is a child action we will omit creating a new telemetry span
	if m.options.IsChildAction() {
		return next(ctx)
	}

	// Note: CommandPath is constructed using the Use member on each command up to the root.
	// It does not contain user input, and is safe for telemetry emission.
	spanCtx, span := telemetry.GetTracer().Start(ctx, events.GetCommandEventName(m.options.CommandPath))

	if m.options.Flags != nil {
		changedFlags := []string{}
		m.options.Flags.VisitAll(func(f *pflag.Flag) {
			if f.Changed {
				changedFlags = append(changedFlags, f.Name)
			}
		})
		telemetry.SetUsageAttributes(fields.CmdFlags.StringSlice(changedFlags))
	}

	telemetry.SetUsageAttributes(fields.CmdArgsCount.Int(len(m.options.Args)))

	defer func() {
		// Include any usage attributes set
		span.SetAttributes(telemetry.GetUsageAttributes()...)
		span.End()
	}()

	result, err := next(spanCtx)
	if err != nil {
		span.SetStatus(codes.Error, "UnknownError")
	}

	return result, err
}

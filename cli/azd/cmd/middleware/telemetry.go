package middleware

import (
	"context"
	"log"

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
	// Note: CommandPath is constructed using the Use member on each command up to the root.
	// It does not contain user input, and is safe for telemetry emission.
	cmdPath := events.GetCommandEventName(m.options.CommandPath)
	spanCtx, span := telemetry.GetTracer().Start(ctx, cmdPath)

	log.Printf("TraceID: %s", span.SpanContext().TraceID())

	if !m.options.IsChildAction() {
		// Set the command name as a baggage item on the span context.
		// This allow inner actions to have command name attached.
		spanCtx = telemetry.SetBaggageInContext(
			spanCtx,
			fields.CmdEntry.String(cmdPath))
	}

	if m.options.Flags != nil {
		changedFlags := []string{}
		m.options.Flags.VisitAll(func(f *pflag.Flag) {
			if f.Changed {
				changedFlags = append(changedFlags, f.Name)
			}
		})
		span.SetAttributes(fields.CmdFlags.StringSlice(changedFlags))
	}

	span.SetAttributes(fields.CmdArgsCount.Int(len(m.options.Args)))

	defer func() {
		// Include any usage attributes set
		span.SetAttributes(telemetry.GetUsageAttributes()...)
		span.End()
	}()

	result, err := next(spanCtx)
	result.TraceID = span.SpanContext().TraceID().String()

	if err != nil {
		span.SetStatus(codes.Error, "UnknownError")
	}

	return result, err
}

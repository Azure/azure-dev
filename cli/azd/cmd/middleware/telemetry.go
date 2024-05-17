package middleware

import (
	"context"
	"errors"
	"log"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/cmd"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/platform"
	"github.com/spf13/pflag"
)

// Telemetry middleware tracks telemetry for the given action
type TelemetryMiddleware struct {
	options            *Options
	lazyPlatformConfig *lazy.Lazy[*platform.Config]
}

// Creates a new Telemetry middleware instance
func NewTelemetryMiddleware(options *Options, lazyPlatformConfig *lazy.Lazy[*platform.Config]) Middleware {
	return &TelemetryMiddleware{
		options:            options,
		lazyPlatformConfig: lazyPlatformConfig,
	}
}

// Invokes the middleware and wraps the action with a telemetry span for telemetry reporting
func (m *TelemetryMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	// Note: CommandPath is constructed using the Use member on each command up to the root.
	// It does not contain user input, and is safe for telemetry emission.
	cmdPath := events.GetCommandEventName(m.options.CommandPath)
	spanCtx, span := tracing.Start(ctx, cmdPath)

	log.Printf("TraceID: %s", span.SpanContext().TraceID())

	if !m.options.IsChildAction(ctx) {
		// Set the command name as a baggage item on the span context.
		// This allow inner actions to have command name attached.
		spanCtx = tracing.SetBaggageInContext(
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

	// Set the platform type when available
	// Valid platform types are validating in the platform config resolver and will error here if not known & valid
	if platformConfig, err := m.lazyPlatformConfig.GetValue(); err == nil && platformConfig != nil {
		span.SetAttributes(fields.PlatformTypeKey.String(string(platformConfig.Type)))
	}

	defer func() {
		// Include any usage attributes set
		span.SetAttributes(tracing.GetUsageAttributes()...)
		span.SetAttributes(fields.PerfInteractTime.Int64(tracing.InteractTimeMs.Load()))
		span.End()
	}()

	result, err := next(spanCtx)
	if result == nil {
		result = &actions.ActionResult{}
	}

	if err != nil {
		cmd.MapError(err, span)

		// We only want to show trace ID for server-related errors,
		// where we have full server logs to troubleshoot from.
		//
		// For client errors, we don't want to show the trace ID, as it is not useful to the user currently.
		var respErr *azcore.ResponseError
		var azureErr *azapi.AzureDeploymentError
		var toolExitErr *exec.ExitError

		if errors.As(err, &respErr) || errors.As(err, &azureErr) ||
			(errors.As(err, &toolExitErr) && toolExitErr.Cmd == "terraform") {
			err = &internal.ErrorWithTraceId{
				Err:     err,
				TraceId: span.SpanContext().TraceID().String(),
			}
		}
	}

	return result, err
}

package telemetry

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/telemetry/baggage"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// SetBaggageInContext sets the given attributes as baggage.
// Baggage attributes are set for the current running span, and for any child spans created.
func SetBaggageInContext(ctx context.Context, attributes ...attribute.KeyValue) context.Context {
	SetAttributesInContext(ctx, attributes...)
	return baggage.ContextWithAttributes(ctx, attributes)
}

// SetAttributesInContext sets the given attributes for the current running span.
func SetAttributesInContext(ctx context.Context, attributes ...attribute.KeyValue) {
	runningSpan := trace.SpanFromContext(ctx)
	runningSpan.SetAttributes(attributes...)
}

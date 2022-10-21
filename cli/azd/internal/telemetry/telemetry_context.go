package telemetry

import (
	"context"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/telemetry/baggage"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/fields"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/resource"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func getEnvironmentAttributes(env *environment.Environment) []attribute.KeyValue {
	return []attribute.KeyValue{
		fields.SubscriptionIdKey.String(env.GetSubscriptionId()),
	}
}

// ContextWithEnvironment sets the environment in context for telemetry purposes.
func ContextWithEnvironment(ctx context.Context, env *environment.Environment) context.Context {
	attributes := getEnvironmentAttributes(env)
	return SetBaggageInContext(ctx, attributes...)
}

// ContextWithTemplate sets the template in context for telemetry purposes.
func ContextWithTemplate(ctx context.Context, templateName string) context.Context {
	return SetBaggageInContext(ctx, fields.TemplateIdKey.String(resource.Sha256Hash(strings.ToLower(templateName))))
}

// TemplateFromContext retrieves the template stored in context.
// If not found, an empty string is returned.
func TemplateFromContext(ctx context.Context) string {
	baggage := baggage.BaggageFromContext(ctx)
	return baggage.Get(fields.TemplateIdKey).AsString()
}

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

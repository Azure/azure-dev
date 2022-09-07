package telemetry

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/telemetry/baggage"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func getEnvironmentAttributes(env *environment.Environment) []attribute.KeyValue {
	return []attribute.KeyValue{
		fields.ObjectIdKey.String(env.GetPrincipalId()),
		fields.SubscriptionIdKey.String(env.GetSubscriptionId()),
		fields.TenantIdKey.String(env.GetTenantId()),
	}
}

// ContextWithEnvironment sets the environment in context for telemetry purposes.
func ContextWithEnvironment(ctx context.Context, env *environment.Environment) context.Context {
	attributes := getEnvironmentAttributes(env)
	return SetAttributesInContext(ctx, attributes...)
}

// ContextWithTemplate sets the template in context for telemetry purposes.
func ContextWithTemplate(ctx context.Context, templateName string) context.Context {
	return SetAttributesInContext(ctx, fields.TemplateIdKey.String(sha256Hash(templateName)))
}

func TemplateFromContext(ctx context.Context) string {
	baggage := baggage.BaggageFromContext(ctx)
	return baggage.Get(fields.TemplateIdKey).AsString()
}

func SetAttributesInContext(ctx context.Context, attributes ...attribute.KeyValue) context.Context {
	// Set the attributes in the current running span so that they are immediately available
	runningSpan := trace.SpanFromContext(ctx)
	runningSpan.SetAttributes(attributes...)

	// Set the attributes as baggage in the context so that they can be propagated to children spans
	return baggage.ContextWithAttributes(ctx, attributes)
}

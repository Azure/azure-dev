// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tracing

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

var tracer = &wrapperTracer{otel.Tracer(fields.ServiceNameAzd)}

// Start creates a span and a context.Context containing the newly-created span.
//
// If the context.Context provided in `ctx` contains a Span then the newly-created
// Span will be a child of that span, otherwise it will be a root span. This behavior
// can be overridden by providing `WithNewRoot()` as a SpanOption, causing the
// newly-created Span to be a root span even if `ctx` contains a Span.
//
// When creating a Span it is recommended to provide all known span attributes using
// the `WithAttributes()` SpanOption as samplers will only have access to the
// attributes provided when a Span is created.
func Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, Span) {
	return tracer.Start(ctx, name, opts...)
}

// SetTracerProviderForTest replaces the TracerProvider backing Start with tp and
// returns a function that restores the previously-configured tracer.
//
// It lets tests capture emitted spans by supplying an in-memory TracerProvider
// without mutating OpenTelemetry's process-global TracerProvider — which is
// shared across the whole process and stays delegated to the first provider set,
// so swapping it is neither isolated nor cleanly reversible. Callers must invoke
// the returned restore function when done (e.g. via t.Cleanup) and are
// responsible for shutting down tp.
func SetTracerProviderForTest(tp trace.TracerProvider) (restore func()) {
	previous := tracer
	tracer = &wrapperTracer{tp.Tracer(fields.ServiceNameAzd)}
	return func() { tracer = previous }
}

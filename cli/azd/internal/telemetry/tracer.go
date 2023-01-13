// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/telemetry/baggage"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/fields"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// It is often valuable to extend functionality of 3rd-party library types.
// Therefore, we provide extension points for OpenTelemetry tracers here.

// Tracer is the creator of Spans.
//
// This is simply trace.Tracer except returning our Span instead of trace.Span.
type Tracer interface {
	Start(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, Span)
}

// Wrapper around trace.Tracer.
type wrapperTracer struct {
	tracer trace.Tracer
}

func (w *wrapperTracer) Start(
	ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, Span) {
	ctx, span := w.tracer.Start(ctx, spanName, opts...)
	// Propagate any baggage in the current context
	baggage := baggage.BaggageFromContext(ctx)
	span.SetAttributes(baggage.Attributes()...)
	return ctx, &wrapperSpan{span}
}

// redefinedSpan is a slightly modified version of trace.Span.
//
// The only change made is to remove functionality around emitting events.
// Events are nested telemetry events that can be fired from a Span.
// We currently do not support this yet (no use case), but this is likely to be changed in the future.
//
// Exact modifications to trace.Span:
//   - Removed AddEvent
//   - Removed RecordError. This creates an error type event
type redefinedSpan interface {
	// End completes the Span. The Span is considered complete and ready to be
	// delivered through the rest of the telemetry pipeline after this method
	// is called. Therefore, updates to the Span are not allowed after this
	// method has been called.
	End(options ...trace.SpanEndOption)

	// IsRecording returns the recording state of the Span. It will return
	// true if the Span is active and events can be recorded.
	IsRecording() bool

	// SpanContext returns the SpanContext of the Span. The returned SpanContext
	// is usable even after the End method has been called for the Span.
	SpanContext() trace.SpanContext

	// SetStatus sets the status of the Span in the form of a code and a
	// description, overriding previous values set. The description is only
	// included in a status when the code is for an error.
	SetStatus(code codes.Code, description string)

	// SetName sets the Span name.
	SetName(name string)

	// SetAttributes sets kv as attributes of the Span. If a key from kv
	// already exists for an attribute of the Span it will be overwritten with
	// the value contained in kv.
	SetAttributes(kv ...attribute.KeyValue)

	// TracerProvider returns a TracerProvider that can be used to generate
	// additional Spans on the same telemetry pipeline as the current Span.
	TracerProvider() trace.TracerProvider
}

// Span is the individual component of a trace. It represents a single named
// and timed operation of a workflow that is traced. A Tracer is used to
// create a Span and it is then up to the operation the Span represents to
// properly end the Span when the operation itself ends.
type Span interface {
	redefinedSpan
}

type wrapperSpan struct {
	span trace.Span
}

// End completes the Span. The Span is considered complete and ready to be
// delivered through the rest of the telemetry pipeline after this method
// is called. Therefore, updates to the Span are not allowed after this
// method has been called.
func (s *wrapperSpan) End(options ...trace.SpanEndOption) {
	s.span.SetAttributes(GetGlobalAttributes()...)
	s.span.End(options...)
}

// IsRecording returns the recording state of the Span. It will return
// true if the Span is active and events can be recorded.
func (s *wrapperSpan) IsRecording() bool {
	return s.span.IsRecording()
}

// SpanContext returns the SpanContext of the Span. The returned SpanContext
// is usable even after the End method has been called for the Span.
func (s *wrapperSpan) SpanContext() trace.SpanContext {
	return s.span.SpanContext()
}

// SetStatus sets the status of the Span in the form of a code and a
// description, overriding previous values set. The description is only
// included in a status when the code is for an error.
func (s *wrapperSpan) SetStatus(code codes.Code, description string) {
	s.span.SetStatus(code, description)
}

// SetName sets the Span name.
func (s *wrapperSpan) SetName(name string) {
	s.span.SetName(name)
}

// SetAttributes sets kv as attributes of the Span. If a key from kv
// already exists for an attribute of the Span it will be overwritten with
// the value contained in kv.
func (s *wrapperSpan) SetAttributes(kv ...attribute.KeyValue) {
	s.span.SetAttributes(kv...)
}

// TracerProvider returns a TracerProvider that can be used to generate
// additional Spans on the same telemetry pipeline as the current Span.
func (s *wrapperSpan) TracerProvider() trace.TracerProvider {
	return s.span.TracerProvider()
}

// GetTracer returns the application tracer for azd.
func GetTracer() Tracer {
	return &wrapperTracer{otel.Tracer(fields.ServiceNameAzd)}
}

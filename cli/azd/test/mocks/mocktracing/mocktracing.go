// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mocktracing

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type Span struct {
	tracetest.SpanStub
}

// SetAttributes implements tracing.Span
func (s *Span) SetAttributes(kv ...attribute.KeyValue) {
	s.Attributes = append(s.SpanStub.Attributes, kv...)
}

// SetName implements tracing.Span
func (s *Span) SetName(name string) {
	s.Name = name
}

// SetStatus implements tracing.Span
func (s *Span) SetStatus(code codes.Code, description string) {
	s.Status = tracesdk.Status{Code: code, Description: description}
}

// SpanContext implements tracing.Span
func (s *Span) SpanContext() trace.SpanContext {
	return s.SpanStub.SpanContext
}

// TracerProvider implements tracing.Span
func (s *Span) TracerProvider() trace.TracerProvider {
	return nil
}

func (e *Span) End(options ...trace.SpanEndOption) {
}

func (e *Span) EndWithStatus(err error, options ...trace.SpanEndOption) {
}

func (e *Span) IsRecording() bool {
	return false
}

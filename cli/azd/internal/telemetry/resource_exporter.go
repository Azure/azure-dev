// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
)

type resourceExporter struct {
	exporter          trace.SpanExporter
	canonicalResource *resource.Resource
}

func newResourceExporter(exporter trace.SpanExporter, canonicalResource *resource.Resource) *resourceExporter {
	if canonicalResource == nil {
		panic("telemetry resource cannot be nil")
	}

	return &resourceExporter{
		exporter:          exporter,
		canonicalResource: canonicalResource,
	}
}

func (e *resourceExporter) ExportSpans(ctx context.Context, spans []trace.ReadOnlySpan) error {
	spansWithCanonicalResource := make([]trace.ReadOnlySpan, len(spans))
	for i, span := range spans {
		spansWithCanonicalResource[i] = spanWithResource{
			ReadOnlySpan:      span,
			canonicalResource: e.canonicalResource,
		}
	}

	return e.exporter.ExportSpans(ctx, spansWithCanonicalResource)
}

func (e *resourceExporter) Shutdown(ctx context.Context) error {
	return e.exporter.Shutdown(ctx)
}

type spanWithResource struct {
	// Embedding preserves the private method on trace.ReadOnlySpan while allowing Resource to be overridden.
	trace.ReadOnlySpan
	canonicalResource *resource.Resource
}

func (s spanWithResource) Resource() *resource.Resource {
	return s.canonicalResource
}

var _ trace.SpanExporter = (*resourceExporter)(nil)
var _ trace.ReadOnlySpan = spanWithResource{}

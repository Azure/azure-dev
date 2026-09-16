// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

func TestResourceExporterReplacesOnlyResource(t *testing.T) {
	canonicalResource := resource.NewSchemaless(attribute.String("canonical.resource", "canonical"))
	ambientResource := resource.NewSchemaless(attribute.String("ambient.resource", "ambient"))

	stub := GetSpanStub()
	stub.Resource = ambientResource
	stub.Events = []sdktrace.Event{{
		Name:       "test-event",
		Time:       stub.EndTime,
		Attributes: []attribute.KeyValue{attribute.String("event.attribute", "event-value")},
	}}
	stub.Links = []sdktrace.Link{{
		SpanContext: stub.SpanContext,
		Attributes:  []attribute.KeyValue{attribute.String("link.attribute", "link-value")},
	}}
	stub.Status = sdktrace.Status{Code: codes.Error, Description: "test status"}
	stub.DroppedAttributes = 1
	stub.DroppedEvents = 2
	stub.DroppedLinks = 3
	stub.ChildSpanCount = 4
	stub.InstrumentationScope = instrumentation.Scope{
		Name:      "test-scope",
		Version:   "1.0.0",
		SchemaURL: "https://example.test/schema",
	}

	originalSpan := stub.Snapshot()
	input := []sdktrace.ReadOnlySpan{originalSpan}
	inputCopy := append([]sdktrace.ReadOnlySpan(nil), input...)

	inner := &testSpanExporter{
		exportSpans: func(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
			require.Len(t, spans, 1)
			require.NotSame(t, &input[0], &spans[0])

			want := tracetest.SpanStubFromReadOnlySpan(originalSpan)
			want.Resource = canonicalResource
			got := tracetest.SpanStubFromReadOnlySpan(spans[0])
			require.Equal(t, want, got)

			return nil
		},
	}

	exporter := newResourceExporter(inner, canonicalResource)
	require.NoError(t, exporter.ExportSpans(t.Context(), input))
	require.Equal(t, inputCopy, input)
	require.Same(t, ambientResource, input[0].Resource())
}

func TestResourceExporterForwardsErrors(t *testing.T) {
	exportErr := errors.New("export failed")
	shutdownErr := errors.New("shutdown failed")
	inner := &testSpanExporter{
		exportSpans: func(context.Context, []sdktrace.ReadOnlySpan) error {
			return exportErr
		},
		shutdown: func(context.Context) error {
			return shutdownErr
		},
	}

	exporter := newResourceExporter(inner, resource.Empty())
	require.ErrorIs(t, exporter.ExportSpans(t.Context(), nil), exportErr)
	require.ErrorIs(t, exporter.Shutdown(t.Context()), shutdownErr)
}

func TestResourceExporterSanitizesApplicationInsights(t *testing.T) {
	t.Setenv(
		"OTEL_RESOURCE_ATTRIBUTES",
		"customer.attribute=customer-value,service.instance.id=customer-instance",
	)
	t.Setenv("OTEL_SERVICE_NAME", "customer-service")

	canonicalResource := resource.NewSchemaless(
		attribute.String("service.name", "azd"),
		attribute.String("canonical.resource", "canonical-value"),
	)
	queue := &InMemoryQueue{}
	appInsightsExporter := NewExporter(queue, "iKey")
	sanitizedExporter := newResourceExporter(appInsightsExporter, canonicalResource)

	ambientResourceObserved := false
	providerExporter := &testSpanExporter{
		exportSpans: func(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
			require.Len(t, spans, 1)
			value, ok := spans[0].Resource().Set().Value(attribute.Key("customer.attribute"))
			ambientResourceObserved = ok && value.AsString() == "customer-value"
			return sanitizedExporter.ExportSpans(ctx, spans)
		},
		shutdown: sanitizedExporter.Shutdown,
	}

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(providerExporter),
		sdktrace.WithResource(canonicalResource),
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		require.NoError(t, tracerProvider.Shutdown(ctx))
	})

	_, span := tracerProvider.Tracer("resource-exporter-test").Start(t.Context(), "test-span")
	span.SetAttributes(attribute.String("ext.test.attribute", "preserved-value"))
	span.End()

	require.True(t, ambientResourceObserved, "expected the provider to merge OTEL_RESOURCE_ATTRIBUTES")
	require.Len(t, queue.queue, 1)

	var envelope struct {
		Data struct {
			BaseData struct {
				Properties map[string]string `json:"properties"`
			} `json:"baseData"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(queue.queue[0], &envelope))
	require.Equal(t, "canonical-value", envelope.Data.BaseData.Properties["canonical.resource"])
	require.Equal(t, "preserved-value", envelope.Data.BaseData.Properties["ext.test.attribute"])
	require.NotContains(t, envelope.Data.BaseData.Properties, "customer.attribute")
	require.NotContains(t, envelope.Data.BaseData.Properties, "service.instance.id")
}

func TestResourceExporterSanitizesOTLP(t *testing.T) {
	t.Setenv(
		"OTEL_RESOURCE_ATTRIBUTES",
		"customer.attribute=customer-value,service.instance.id=customer-instance",
	)
	t.Setenv("OTEL_SERVICE_NAME", "customer-service")

	type receivedRequest struct {
		path string
		body []byte
	}
	requests := make(chan receivedRequest, 1)
	responseBody, err := proto.Marshal(&collectortracepb.ExportTraceServiceResponse{})
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			http.Error(w, readErr.Error(), http.StatusInternalServerError)
			return
		}

		requests <- receivedRequest{path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(responseBody)
	}))
	t.Cleanup(server.Close)

	httpExporter, err := otlptracehttp.New(
		t.Context(),
		otlptracehttp.WithEndpoint(strings.TrimPrefix(server.URL, "http://")),
		otlptracehttp.WithInsecure(),
		otlptracehttp.WithCompression(otlptracehttp.NoCompression),
	)
	require.NoError(t, err)

	canonicalResource := resource.NewSchemaless(
		attribute.String("service.name", "azd"),
		attribute.String("canonical.resource", "canonical-value"),
	)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(newResourceExporter(httpExporter, canonicalResource)),
		sdktrace.WithResource(canonicalResource),
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		require.NoError(t, tracerProvider.Shutdown(ctx))
	})

	_, span := tracerProvider.Tracer("resource-exporter-test").Start(t.Context(), "test-span")
	span.SetAttributes(attribute.String("ext.test.attribute", "preserved-value"))
	span.End()

	var request receivedRequest
	select {
	case request = <-requests:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for OTLP export")
	}
	require.Equal(t, "/v1/traces", request.path)

	var exportRequest collectortracepb.ExportTraceServiceRequest
	require.NoError(t, proto.Unmarshal(request.body, &exportRequest))
	require.Len(t, exportRequest.ResourceSpans, 1)

	resourceAttributes := otlpStringAttributes(exportRequest.ResourceSpans[0].Resource.Attributes)
	require.Equal(t, "azd", resourceAttributes["service.name"])
	require.Equal(t, "canonical-value", resourceAttributes["canonical.resource"])
	require.NotContains(t, resourceAttributes, "customer.attribute")
	require.NotContains(t, resourceAttributes, "service.instance.id")

	require.Len(t, exportRequest.ResourceSpans[0].ScopeSpans, 1)
	require.Len(t, exportRequest.ResourceSpans[0].ScopeSpans[0].Spans, 1)
	spanAttributes := otlpStringAttributes(exportRequest.ResourceSpans[0].ScopeSpans[0].Spans[0].Attributes)
	require.Equal(t, "preserved-value", spanAttributes["ext.test.attribute"])
}

type testSpanExporter struct {
	exportSpans func(context.Context, []sdktrace.ReadOnlySpan) error
	shutdown    func(context.Context) error
}

func (e *testSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if e.exportSpans == nil {
		return nil
	}

	return e.exportSpans(ctx, spans)
}

func (e *testSpanExporter) Shutdown(ctx context.Context) error {
	if e.shutdown == nil {
		return nil
	}

	return e.shutdown(ctx)
}

func otlpStringAttributes(attributes []*commonpb.KeyValue) map[string]string {
	result := make(map[string]string, len(attributes))
	for _, kv := range attributes {
		result[kv.Key] = kv.Value.GetStringValue()
	}

	return result
}

var _ sdktrace.SpanExporter = (*testSpanExporter)(nil)

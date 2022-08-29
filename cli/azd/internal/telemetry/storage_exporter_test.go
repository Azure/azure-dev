package telemetry

import (
	"context"
	"testing"
	"time"

	appinsightsexporter "github.com/azure/azure-dev/cli/azd/internal/telemetry/appinsights-exporter"
	"github.com/microsoft/ApplicationInsights-Go/appinsights/contracts"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.6.1"
	"go.opentelemetry.io/otel/trace"
)

func TestExportSpans(t *testing.T) {
	queue := InMemoryQueue{[][]byte{}}
	exporter := NewExporter(&queue, "iKey")
	assert.False(t, exporter.ExportedAny())

	spans := []tracesdk.ReadOnlySpan{}
	spans = append(spans, GetSpanStub().Snapshot())
	spans = append(spans, GetSpanStub().Snapshot())
	spans = append(spans, GetSpanStub().Snapshot())

	err := exporter.ExportSpans(context.Background(), spans)
	assert.NoError(t, err)
	assert.Len(t, queue.queue, 1)
	assert.True(t, exporter.ExportedAny())

	var telemetryItemsQueued appinsightsexporter.TelemetryItems
	telemetryItemsQueued.Deserialize(queue.queue[0])
	assert.Len(t, []contracts.Envelope(telemetryItemsQueued), len(spans))
}

type InMemoryQueue struct {
	queue [][]byte
}

func (q *InMemoryQueue) Enqueue(message []byte) error {
	q.queue = append(q.queue, message)
	return nil
}

func GetSpanStub() tracetest.SpanStub {
	traceId, _ := trace.TraceIDFromHex("68f1c4f4ef5346e69d7f196761d10c68")
	spanId, _ := trace.SpanIDFromHex("7fbdc197a52f4825877ddd46e4ec7f6c")
	parentSpanId, _ := trace.SpanIDFromHex("c73af03ea9684e67ab71bcff33b04a89")

	startTime, _ := time.Parse(time.RFC3339, "2006-01-02T15:04:05Z")
	endTime := startTime.Add(time.Second * 10)

	return tracetest.SpanStub{
		Name: "DefaultSpan",
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: traceId,
			SpanID:  spanId,
		}),
		Parent: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: traceId,
			SpanID:  parentSpanId,
		}),
		SpanKind:  trace.SpanKindUnspecified,
		StartTime: startTime,
		EndTime:   endTime,
		Attributes: []attribute.KeyValue{
			// Types
			attribute.Bool("Bool", true),
			attribute.String("String", "StringVal"),
			attribute.Int64("Int64", 12345),
			attribute.Float64("Float64", 12345.12345),
			attribute.Float64Slice("Float64Slice", []float64{1.0, 2.0, 3.0}),
			attribute.Int64Slice("Int64Slice", []int64{1, 2, 3}),
			attribute.StringSlice("StringSlice", []string{"1", "2", "3"}),
			attribute.BoolSlice("BoolSlice", []bool{true, false, true}),

			// Special AI fields
			attribute.String(contracts.UserId, "UserId"),
			attribute.String(contracts.SessionId, "SessionId"),
		},
		Status: tracesdk.Status{Code: codes.Ok},
		Resource: resource.NewWithAttributes(
			semconv.SchemaURL,
			attribute.String("environment", "unit-test"),

			// Special AI fields
			attribute.String(contracts.ApplicationVersion, "0.1.0"),
		),
	}
}

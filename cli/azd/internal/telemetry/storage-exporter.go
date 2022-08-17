package telemetry

import (
	"context"

	appinsightsexporter "github.com/azure/azure-dev/cli/azd/internal/telemetry/appinsights-exporter"
	"go.opentelemetry.io/otel/sdk/trace"
)

type simpleQueue interface {
	Enqueue(message []byte) error
}

type Exporter struct {
	queue              simpleQueue
	instrumentationKey string
}

func NewExporter(queue simpleQueue, instrumentationKey string) *Exporter {
	return &Exporter{
		queue:              queue,
		instrumentationKey: instrumentationKey,
	}
}

func (e *Exporter) ExportSpans(ctx context.Context, spans []trace.ReadOnlySpan) error {
	var items appinsightsexporter.TelemetryItems
	for _, span := range spans {
		envelope := appinsightsexporter.SpanToEnvelope(span)
		envelope.IKey = e.instrumentationKey

		items = append(items, *envelope)
	}

	e.queue.Enqueue(items.Serialize())

	return nil
}

func (e *Exporter) Shutdown(ctx context.Context) error {
	return nil
}

// MarshalLog is the marshaling function used by the logging system to represent this exporter.
func (e *Exporter) MarshalLog() interface{} {
	return struct {
		Type string
	}{
		Type: "appinsights",
	}
}

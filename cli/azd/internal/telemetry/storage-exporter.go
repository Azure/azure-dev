package telemetry

import (
	"context"

	appinsightsexporter "github.com/azure/azure-dev/cli/azd/internal/telemetry/appinsights-exporter"
	"go.opentelemetry.io/otel/sdk/trace"
)

type simpleQueue interface {
	Enqueue(message []byte) error
}

// Exporter is an implementation of trace.SpanSyncer that writes spans into a storage queue in ApplicationInsights format.
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

// ExportSpans writes spans to the storage queue in AppInsights format.
func (e *Exporter) ExportSpans(ctx context.Context, spans []trace.ReadOnlySpan) error {
	var items appinsightsexporter.TelemetryItems
	for _, span := range spans {
		envelope := appinsightsexporter.SpanToEnvelope(span)
		envelope.IKey = e.instrumentationKey

		items = append(items, *envelope)
	}

	return e.queue.Enqueue(items.Serialize())
}

// Shutdown is called to stop the exporter, it performs no action.
func (e *Exporter) Shutdown(ctx context.Context) error {
	return nil
}

// MarshalLog is the marshaling function used by the logging system to represent this exporter.
func (e *Exporter) MarshalLog() interface{} {
	return struct {
		Type string
	}{
		Type: "appinsightsstorage",
	}
}

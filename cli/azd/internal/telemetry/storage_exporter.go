package telemetry

import (
	"context"

	appinsightsexporter "github.com/azure/azure-dev/cli/azd/internal/telemetry/appinsights-exporter"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/atomic"
)

type simpleQueue interface {
	Enqueue(message []byte) error
}

// Exporter is an implementation of trace.SpanExporter that writes spans into a
// storage queue in ApplicationInsights format.
type Exporter struct {
	queue simpleQueue

	anyExported        *atomic.Bool
	instrumentationKey string
}

func NewExporter(queue simpleQueue, instrumentationKey string) *Exporter {
	return &Exporter{
		queue:              queue,
		instrumentationKey: instrumentationKey,
		anyExported:        atomic.NewBool(false),
	}
}

// ExportSpans writes spans to the storage queue in AppInsights format.
func (e *Exporter) ExportSpans(ctx context.Context, spans []trace.ReadOnlySpan) error {
	var items appinsightsexporter.TelemetryItems
	for _, span := range spans {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			envelope := appinsightsexporter.SpanToEnvelope(span)
			envelope.IKey = e.instrumentationKey

			items = append(items, *envelope)
		}
	}

	if len(items) == 0 {
		return nil
	}

	message := items.Serialize()

	// Add a small, immediate retry loop in case of transient failures with disk storage.
	// To avoid any delay while telemetry is flushed during application exit, no backoff is added.
	var err error
	for i := 0; i < 3; i++ {
		err = e.queue.Enqueue(message)
		if err == nil {
			break
		}
	}

	if err == nil {
		e.anyExported.Store(true)
	}
	return err
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

func (e *Exporter) ExportedAny() bool {
	return e.anyExported.Load()
}

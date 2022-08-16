package telemetry

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"time"

	appinsightsexporter "github.com/azure/azure-dev/cli/azd/internal/telemetry/appinsights-exporter"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
)

const telemetryItemExtension = ".trn"

type TelemetrySystem struct {
	storageQueue   StorageQueue
	tracerProvider *trace.TracerProvider
}

var once sync.Once
var instance *TelemetrySystem

func getStorageDirectory() (string, error) {
	user, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("could not determine current user: %w", err)
	}

	telemetryDir := filepath.Join(user.HomeDir, ".azd", "telemetry")
	return telemetryDir, nil
}

func newResource() *resource.Resource {
	r, _ := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
		),
	)
	return r
}

func IsTelemetryEnabled() bool {
	// the equivalent of AZURE_CORE_COLLECT_TELEMETRY
	return os.Getenv("AZURE_DEV_COLLECT_TELEMETRY") != "no"
}

func GetTelemetrySystem() *TelemetrySystem {
	once.Do(func() {
		telemetrySystem, err := initialize()
		if err != nil {
			fmt.Printf("failed to initialize telemetry: %v\n", err)
		} else {
			instance = telemetrySystem
		}
	})

	return instance
}

func initialize() (*TelemetrySystem, error) {
	if !IsTelemetryEnabled() {
		log.Println("telemetry is disabled by user and will not be initialized.")
		return nil, nil
	}

	appinsightsexporter.SetListener(func(msg string) {
		fmt.Printf("[%s] %s\n", time.Now().Format(time.UnixDate), msg)
	})

	storageDirectory, err := getStorageDirectory()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve storage directory: %w", err)
	}

	storageQueue, err := NewStorageQueue(storageDirectory, telemetryItemExtension)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage queue: %w", err)
	}

	exporter := NewExporter(storageQueue)

	tp := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(newResource()),
	)
	otel.SetTracerProvider(tp)
	// downloadOperation(context.Background())

	return &TelemetrySystem{
		storageQueue:   *storageQueue,
		tracerProvider: tp,
	}, nil
}

func (ts *TelemetrySystem) Shutdown(ctx context.Context) {
	instance.tracerProvider.Shutdown(ctx)
}

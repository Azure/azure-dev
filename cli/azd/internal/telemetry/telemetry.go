package telemetry

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"sync"

	"github.com/azure/azure-dev/cli/azd/internal"
	appinsightsexporter "github.com/azure/azure-dev/cli/azd/internal/telemetry/appinsights-exporter"
	"github.com/gofrs/flock"
	"github.com/microsoft/ApplicationInsights-Go/appinsights"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
)

const telemetryItemExtension = ".trn"
const devInstrumentationKey = "d3b9c006-3680-4300-9862-35fce9ac66c7"
const prodInstrumentationKey = ""

type TelemetrySystem struct {
	storageQueue   *StorageQueue
	tracerProvider *trace.TracerProvider

	instrumentationKey string
	telemetryDirectory string
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
	// Feature guard: To be enabled once dependencies are met for production
	isDev := internal.IsDevVersion()
	if !isDev {
		return nil, nil
	}

	if !IsTelemetryEnabled() {
		log.Println("telemetry is disabled by user and will not be initialized.")
		return nil, nil
	}

	appinsightsexporter.SetListener(func(msg string) {
		log.Println(msg)
	})

	storageDirectory, err := getStorageDirectory()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve storage directory: %w", err)
	}

	storageQueue, err := NewStorageQueue(storageDirectory, telemetryItemExtension)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage queue: %w", err)
	}

	var instrumentationKey string
	if isDev {
		instrumentationKey = devInstrumentationKey
	} else {
		instrumentationKey = prodInstrumentationKey
	}

	exporter := NewExporter(storageQueue, instrumentationKey)

	tp := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(newResource()),
	)
	otel.SetTracerProvider(tp)

	return &TelemetrySystem{
		storageQueue:       storageQueue,
		tracerProvider:     tp,
		instrumentationKey: instrumentationKey,
		telemetryDirectory: storageDirectory,
	}, nil
}

// Flushes all ongoing telemetry and shuts down
func (ts *TelemetrySystem) Shutdown(ctx context.Context) {
	instance.tracerProvider.Shutdown(ctx)
}

// Returns the storage queue instance
func (ts *TelemetrySystem) GetStorageQueue() *StorageQueue {
	return instance.storageQueue
}

func (ts *TelemetrySystem) NewUploader(enableDebugLogging bool) *Uploader {
	config := appinsights.NewTelemetryConfiguration(ts.instrumentationKey)
	transmitter := appinsightsexporter.NewTransmitter(config.EndpointUrl, nil)

	uploader := NewUploader(ts.GetStorageQueue(), transmitter, enableDebugLogging)
	return uploader
}

func (ts *TelemetrySystem) RunBackgroundUpload(ctx context.Context, enableDebugLogging bool) error {
	fileLock := flock.New(filepath.Join(ts.telemetryDirectory, "upload.lock"))
	locked, err := fileLock.TryLock()

	if err != nil {
		return fmt.Errorf("failed to acquire upload lock %w", err)
	}

	if locked {
		uploader := ts.NewUploader(enableDebugLogging)
		queue := ts.GetStorageQueue()
		upload := make(chan error)

		go uploader.Upload(ctx, upload)

		ctx, cancelCleanup := context.WithCancel(ctx)
		go queue.Cleanup(ctx)

		err := <-upload
		cancelCleanup()

		return err
	}

	log.Println("Upload already in progress. Exiting.")
	return nil
}

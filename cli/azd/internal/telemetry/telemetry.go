// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package telemetry provides functionality for emitting telemetry in azd.
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

	"github.com/azure/azure-dev/cli/azd/internal"
	appinsightsexporter "github.com/azure/azure-dev/cli/azd/internal/telemetry/appinsights-exporter"
	"github.com/benbjohnson/clock"
	"github.com/gofrs/flock"
	"github.com/microsoft/ApplicationInsights-Go/appinsights"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
)

const azdAppName = "azd"

// the equivalent of AZURE_CORE_COLLECT_TELEMETRY
const collectTelemetryEnvVar = "AZURE_DEV_COLLECT_TELEMETRY"

const telemetryItemExtension = ".trn"
const (
	devInstrumentationKey  = "d3b9c006-3680-4300-9862-35fce9ac66c7"
	prodInstrumentationKey = ""
)

const appInsightsMaxIngestionDelay = time.Duration(48) * time.Hour

type TelemetrySystem struct {
	storageQueue   *StorageQueue
	tracerProvider *trace.TracerProvider
	exporter       *Exporter

	instrumentationKey string
	telemetryDirectory string
}

var once sync.Once
var instance *TelemetrySystem

func getTelemetryDirectory() (string, error) {
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
			semconv.ServiceNameKey.String(azdAppName),
			semconv.ServiceVersionKey.String(internal.GetVersionNumber()),
		),
	)
	return r
}

func IsTelemetryEnabled() bool {
	return os.Getenv(collectTelemetryEnvVar) != "no"
}

// Returns the singleton TelemetrySystem instance.
// Returns nil if telemetry failed to initialize, or user has disabled telemetry.
func GetTelemetrySystem() *TelemetrySystem {
	once.Do(func() {
		telemetrySystem, err := initialize()
		if err != nil {
			log.Printf("failed to initialize telemetry: %v\n", err)
		} else {
			instance = telemetrySystem
		}
	})

	return instance
}

func initialize() (*TelemetrySystem, error) {
	// Feature guard: Disable for production until dependencies are met in production
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

	telemetryDir, err := getTelemetryDirectory()
	if err != nil {
		return nil, fmt.Errorf("failed to determine storage directory: %w", err)
	}

	storageQueue, err := NewStorageQueue(telemetryDir, telemetryItemExtension, appInsightsMaxIngestionDelay)
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
		exporter:           exporter,
		instrumentationKey: instrumentationKey,
		telemetryDirectory: telemetryDir,
	}, nil
}

// Flushes all ongoing telemetry and shuts down telemetry
func (ts *TelemetrySystem) Shutdown(ctx context.Context) error {
	return instance.tracerProvider.Shutdown(ctx)
}

// Returns the telemetry queue instance.
func (ts *TelemetrySystem) GetTelemetryQueue() Queue {
	return instance.storageQueue
}

// Returns true if any telemetry was emitted.
func (ts *TelemetrySystem) EmittedAnyTelemetry() bool {
	return ts.exporter.ExportedAny()
}

func (ts *TelemetrySystem) NewUploader(enableDebugLogging bool) Uploader {
	config := appinsights.NewTelemetryConfiguration(ts.instrumentationKey)
	transmitter := appinsightsexporter.NewTransmitter(config.EndpointUrl, nil)

	uploader := NewUploader(ts.GetTelemetryQueue(), transmitter, clock.New(), enableDebugLogging)
	return uploader
}

func (ts *TelemetrySystem) RunBackgroundUpload(ctx context.Context, enableDebugLogging bool) error {
	fileLock, locked, err := ts.tryUploadLock()
	if err != nil {
		return fmt.Errorf("failed to acquire upload lock %w", err)
	}

	if locked {
		defer func() { _ = fileLock.Unlock() }()
		uploader := ts.NewUploader(enableDebugLogging)
		queue := ts.storageQueue
		uploadResult := make(chan error)
		cleanupDone := make(chan struct{})

		go uploader.Upload(ctx, uploadResult)

		ctx, cancelCleanup := context.WithCancel(ctx)
		go queue.Cleanup(ctx, cleanupDone)

		err := <-uploadResult

		// Provide some minimum guarantee of cleanup running
		c := clock.New()
		select {
		case <-c.After(time.Duration(5) * time.Second):
		case <-cleanupDone:
		}
		cancelCleanup()

		if err != nil {
			log.Printf("failed to upload telemetry: %v", err)
		}
		return err
	}

	log.Println("Upload already in progress. Exiting.")
	return nil
}

func (ts *TelemetrySystem) tryUploadLock() (*flock.Flock, bool, error) {
	fileLock := flock.New(filepath.Join(ts.telemetryDirectory, "upload.lock"))
	locked, err := fileLock.TryLock()
	return fileLock, locked, err
}

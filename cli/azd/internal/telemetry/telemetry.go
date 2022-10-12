// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package telemetry provides functionality for emitting telemetry in azd.
package telemetry

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	appinsightsexporter "github.com/azure/azure-dev/cli/azd/internal/telemetry/appinsights-exporter"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/resource"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/benbjohnson/clock"
	"github.com/gofrs/flock"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
)

// the equivalent of AZURE_CORE_COLLECT_TELEMETRY
const collectTelemetryEnvVar = "AZURE_DEV_COLLECT_TELEMETRY"

const telemetryItemExtension = ".trn"

//nolint:lll
const (
	devConnectionString  = "InstrumentationKey=cf5f7d89-5383-47a8-8d27-ad237c3613d9;IngestionEndpoint=https://westus-0.in.applicationinsights.azure.com/;LiveEndpoint=https://westus.livediagnostics.monitor.azure.com/"
	prodConnectionString = "InstrumentationKey=a9e6fa10-a9ac-4525-8388-22d39336ecc2;IngestionEndpoint=https://centralus-2.in.applicationinsights.azure.com/;LiveEndpoint=https://centralus.livediagnostics.monitor.azure.com/"
)

const appInsightsMaxIngestionDelay = time.Duration(48) * time.Hour

type TelemetrySystem struct {
	storageQueue   *StorageQueue
	tracerProvider *trace.TracerProvider
	exporter       *Exporter

	config             appinsightsexporter.EndpointConfig
	telemetryDirectory string
}

var once sync.Once
var instance *TelemetrySystem

func getTelemetryDirectory() (string, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return "", fmt.Errorf("could not determine current user: %w", err)
	}

	telemetryDir := filepath.Join(configDir, "telemetry")
	return telemetryDir, nil
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

	var connectionString string
	if internal.IsNonProdVersion() {
		connectionString = devConnectionString
	} else {
		connectionString = prodConnectionString
	}
	config, err := appinsightsexporter.NewEndpointConfig(connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse appInsights connection string: %w", err)
	}

	exporter := NewExporter(storageQueue, config.InstrumentationKey)

	tp := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(resource.New()),
	)
	otel.SetTracerProvider(tp)

	return &TelemetrySystem{
		storageQueue:       storageQueue,
		tracerProvider:     tp,
		exporter:           exporter,
		config:             config,
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
	transmitter := appinsightsexporter.NewTransmitter(ts.config.EndpointUrl, nil)

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

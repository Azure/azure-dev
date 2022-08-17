package telemetry

import (
	"log"
	"net/http"
	"time"

	appinsightsexporter "github.com/azure/azure-dev/cli/azd/internal/telemetry/appinsights-exporter"
	"github.com/microsoft/ApplicationInsights-Go/appinsights"
)

var maxRetryCount = 5

type Uploader struct {
	transmitter    appinsightsexporter.Transmitter
	telemetryQueue Queue
	isDebugMode    bool
}

func NewUploader(telemetryQueue Queue, instrumentationKey string, isDebugMode bool, client *http.Client) *Uploader {
	config := appinsights.NewTelemetryConfiguration(instrumentationKey)
	transmitter := appinsightsexporter.NewTransmitter(config.EndpointUrl, client)

	return &Uploader{
		transmitter:    transmitter,
		telemetryQueue: telemetryQueue,
		isDebugMode:    isDebugMode,
	}
}

func (u *Uploader) Upload() {
	for {
		done := u.uploadItem()

		if done {
			return
		}
	}
}

func (u *Uploader) uploadItem() bool {
	item, err := u.telemetryQueue.Peek()
	if err != nil {
		log.Printf("Error reading item: %v\n", err)
		return false
	}

	if item == nil {
		return true
	}

	u.transmit(item)
	u.telemetryQueue.Remove(item)

	return false
}

func (u *Uploader) transmit(item *StoredItem) {
	payload := item.Message()
	var telemetryItems appinsightsexporter.TelemetryItems
	if u.isDebugMode {
		// Always deserialize so we can get better error messages
		telemetryItems.Deserialize(payload)
	}
	result, err := u.transmitter.Transmit(payload, telemetryItems)

	if err != nil {
		retryAttempts := item.RetryCount() + 1
		if retryAttempts <= maxRetryCount {
			u.telemetryQueue.EnqueueWithDelay(payload, time.Duration(retryAttempts*500)*time.Millisecond, retryAttempts)
		} else {
			log.Printf("Failed to send %v after %d attempts.\n", item.fileName, retryAttempts)
		}
	} else if result.CanRetry() {
		retryAttempts := item.RetryCount() + 1
		var delayDuration time.Duration

		if retryAttempts > maxRetryCount {
			log.Printf("Failed to send %v after %d attempts.\n", item.fileName, retryAttempts)
			return
		}

		if result.RetryAfter() != nil {
			delayDuration = time.Until(*result.RetryAfter())
		} else {
			delayDuration = time.Duration(500) * time.Millisecond
		}

		if result.IsPartialSuccess() {
			var telemetryItems appinsightsexporter.TelemetryItems
			telemetryItems.Deserialize(payload)
			newPayload, _ := result.GetRetryItems(payload, telemetryItems)
			u.telemetryQueue.EnqueueWithDelay(newPayload, delayDuration, retryAttempts)
		} else {
			u.telemetryQueue.EnqueueWithDelay(payload, delayDuration, retryAttempts)
		}
	} else {
		if result.IsFailure() {
			log.Printf("Failed to transmit item %s with non-retriable status code %d\n", item.fileName, result.StatusCode())
		}
	}
}

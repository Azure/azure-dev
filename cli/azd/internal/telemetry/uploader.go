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
}

func NewUploader(telemetryQueue Queue, instrumentationKey string, client *http.Client) *Uploader {
	config := appinsights.NewTelemetryConfiguration(instrumentationKey)
	transmitter := appinsightsexporter.NewTransmitter(config.EndpointUrl, client)

	return &Uploader{
		transmitter:    transmitter,
		telemetryQueue: telemetryQueue,
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
	defer u.telemetryQueue.Remove(item)
	if err != nil {
		log.Printf("Error reading item: %v\n", err)
		return false
	}

	if item == nil {
		return true
	}

	u.transmit(item)
	return false
}

func (u *Uploader) transmit(item *StoredItem) {
	payload := item.Message()
	result, err := u.transmitter.Transmit(payload, appinsightsexporter.TelemetryItems{})

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

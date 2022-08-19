package telemetry

import (
	"context"
	"log"
	"time"

	appinsightsexporter "github.com/azure/azure-dev/cli/azd/internal/telemetry/appinsights-exporter"
	"github.com/benbjohnson/clock"
	"github.com/sethvargo/go-retry"
)

const maxRetryCount = 3
const maxReadFailCount = 5
const maxRemoveFailCount = 5

type Uploader struct {
	transmitter    appinsightsexporter.Transmitter
	telemetryQueue Queue
	clock          clock.Clock

	isDebugMode bool
}

func NewUploader(telemetryQueue Queue, transmitter appinsightsexporter.Transmitter, clock clock.Clock, isDebugMode bool) *Uploader {
	return &Uploader{
		transmitter:    transmitter,
		telemetryQueue: telemetryQueue,
		clock:          clock,
		isDebugMode:    isDebugMode,
	}
}

func (u *Uploader) Upload(ctx context.Context, result chan (error)) {
	for {
		select {
		case <-ctx.Done():
			result <- ctx.Err()
			return
		default:
			done, err := u.uploadNextItem()

			if done {
				result <- err
				return
			}
		}
	}
}

func (u *Uploader) uploadNextItem() (bool, error) {
	ctx := context.Background()
	item, err := u.reliablePeek(ctx)

	if err != nil {
		log.Printf("FATAL: Terminating upload after %d consecutive read failures, err: %v", maxReadFailCount, err)
		return true, err
	}

	if item == nil {
		return true, nil
	}

	u.transmit(item)
	err = u.reliableRemove(ctx, item)

	if err != nil {
		log.Printf("FATAL: Terminating upload after %d consecutive remove failures, err: %v", maxRemoveFailCount, err)
		return true, err
	}

	return false, nil
}

func (u *Uploader) reliablePeek(ctx context.Context) (*StoredItem, error) {
	var item *StoredItem
	err := retry.Do(ctx, retry.WithMaxRetries(maxReadFailCount, retry.NewConstant(time.Duration(300)*time.Millisecond)), func(ctx context.Context) error {
		peekItem, err := u.telemetryQueue.Peek()

		if err != nil {
			return retry.RetryableError(err)
		}

		item = peekItem
		return nil
	})

	if err != nil && ctx.Err() != nil {
		// Attempt fallback - remove and retry Peek
		err = u.reliableRemove(ctx, item)

		if err != nil {
			item, err = u.telemetryQueue.Peek()
		}
	}

	return item, err
}

func (u *Uploader) reliableRemove(ctx context.Context, item *StoredItem) error {
	return retry.Do(ctx, retry.WithMaxRetries(maxRemoveFailCount, retry.NewConstant(time.Duration(300)*time.Millisecond)), func(ctx context.Context) error {
		return retry.RetryableError(u.telemetryQueue.Remove(item))
	})
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

		if result.RetryAfter != nil {
			delayDuration = u.clock.Until(*result.RetryAfter)
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
			log.Printf("Failed to transmit item %s with non-retriable status code %d\n", item.fileName, result.StatusCode)
		}
	}
}

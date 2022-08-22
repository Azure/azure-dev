package telemetry

import (
	"context"
	"log"
	"time"

	appinsightsexporter "github.com/azure/azure-dev/cli/azd/internal/telemetry/appinsights-exporter"
	"github.com/benbjohnson/clock"
	"github.com/sethvargo/go-retry"
)

const (
	maxRetryCount      = 3
	maxReadFailCount   = 5
	maxRemoveFailCount = 5
)

var (
	// Optimistic first retry delay
	firstTransmitRetryDelay   = time.Duration(100) * time.Millisecond
	defaultTransmitRetryDelay = time.Duration(2) * time.Second
	defaultThrottleDuration   = time.Duration(5) * time.Second
)

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

// Uploads all items that are currently in the telemetry queue.
// This function returns when no items remain in the queue.
// An error is only returned if there is a fatal, persistent error with reading the queue.
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
		// Deserialize so we can get better error messages
		telemetryItems.Deserialize(payload)
	}
	result, err := u.transmitter.Transmit(payload, telemetryItems)
	if err == nil && result != nil && result.IsSuccess() {
		return
	}

	attempts := item.RetryCount() + 1

	if err != nil || result == nil {
		if attempts > maxRetryCount {
			log.Printf("Failed to send %v after %d attempts.\n", item.fileName, maxRetryCount)
			return
		}

		u.telemetryQueue.EnqueueWithDelay(payload, defaultTransmitRetryDelay, attempts)
	} else if result.CanRetry() {
		if attempts > maxRetryCount {
			log.Printf("Failed to send %v after %d attempts.\n", item.fileName, maxRetryCount)
			return
		}

		if result.IsThrottled() {
			var throttleDuration time.Duration
			if result.RetryAfter != nil {
				throttleDuration = u.clock.Until(*result.RetryAfter)
			} else {
				throttleDuration = defaultThrottleDuration
			}

			log.Printf("Upload is being throttled. Resuming upload in %v.", throttleDuration)
			time.Sleep(throttleDuration)
		}

		retryDelay := u.calculateRetryDelay(result.RetryAfter, attempts)

		if result.IsPartialSuccess() {
			var telemetryItems appinsightsexporter.TelemetryItems
			telemetryItems.Deserialize(payload)
			newPayload, _ := result.GetRetryItems(payload, telemetryItems)
			u.telemetryQueue.EnqueueWithDelay(newPayload, retryDelay, attempts)
		} else {
			u.telemetryQueue.EnqueueWithDelay(payload, retryDelay, attempts)
		}
	} else {
		log.Printf("Failed to transmit item %s with non-retriable status code %d\n", item.fileName, result.StatusCode)
	}
}

func (u *Uploader) calculateRetryDelay(retryAfter *time.Time, attempts int) time.Duration {
	if retryAfter != nil {
		return u.clock.Until(*retryAfter)
	} else if attempts == 1 {
		return firstTransmitRetryDelay
	} else {
		return defaultTransmitRetryDelay
	}
}

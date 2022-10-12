package telemetry

import (
	"context"
	"fmt"
	"log"
	"time"

	appinsightsexporter "github.com/azure/azure-dev/cli/azd/internal/telemetry/appinsights-exporter"
	"github.com/benbjohnson/clock"
	"github.com/sethvargo/go-retry"
)

const (
	maxRetryCount       = 3
	maxStorageFailCount = 5
)

var (
	// Optimistic first retry delay
	firstTransmitRetryDelay   = time.Duration(100) * time.Millisecond
	defaultTransmitRetryDelay = time.Duration(2) * time.Second
	storageQueueRetryDelay    = time.Duration(300) * time.Millisecond

	defaultThrottleDuration = time.Duration(5) * time.Second
)

type Uploader interface {
	Upload(ctx context.Context, result chan (error))
}

type TelemetryUploader struct {
	transmitter    appinsightsexporter.Transmitter
	telemetryQueue Queue
	clock          clock.Clock

	isDebugMode bool
}

func NewUploader(
	telemetryQueue Queue,
	transmitter appinsightsexporter.Transmitter,
	clock clock.Clock,
	isDebugMode bool,
) *TelemetryUploader {
	return &TelemetryUploader{
		transmitter:    transmitter,
		telemetryQueue: telemetryQueue,
		clock:          clock,
		isDebugMode:    isDebugMode,
	}
}

// Uploads all items that are currently in the telemetry queue.
// This function returns when no items remain in the queue.
// An error is only returned if there is a fatal, persistent error with reading the queue.
func (u *TelemetryUploader) Upload(ctx context.Context, result chan (error)) {
	for {
		select {
		case <-ctx.Done():
			result <- ctx.Err()
			return
		default:
			done, err := u.uploadNextItem(ctx)

			if done {
				result <- err
				return
			}
		}
	}
}

func (u *TelemetryUploader) uploadNextItem(ctx context.Context) (bool, error) {
	item, err := u.reliablePeek(ctx)

	if err != nil {
		return true, fmt.Errorf("failed to peek after %d attempts: %w", maxStorageFailCount, err)
	}

	if item == nil {
		return true, nil
	}

	u.transmit(ctx, item)
	err = u.reliableRemove(ctx, item)

	if err != nil {
		return true, fmt.Errorf("failed to remove after %d attempts: %w", maxStorageFailCount, err)
	}

	return false, nil
}

func (u *TelemetryUploader) reliablePeek(ctx context.Context) (*StoredItem, error) {
	return u.reliablePeekWithRemoveFallback(ctx)
}

// reliablePeekWithRemoveFallback calls Peek() with removal fallback.
// If multiple retry attempts fail, the item is assumed to be corrupted.
// A Remove() and a subsequent Peek() is attempted to fetch the next item.
// If the fallback fails, the error is returned.
func (u *TelemetryUploader) reliablePeekWithRemoveFallback(ctx context.Context) (*StoredItem, error) {
	item, err := u.reliablePeekOnly(ctx)

	if err != nil && ctx.Err() != nil {
		// Attempt fallback - remove and retry Peek
		err = u.reliableRemove(ctx, item)

		if err != nil {
			item, err = u.reliablePeekOnly(ctx)
		}
	}

	return item, err
}

// reliablePeekOnly calls Peek() only.
func (u *TelemetryUploader) reliablePeekOnly(ctx context.Context) (*StoredItem, error) {
	var item *StoredItem
	err := retry.Do(
		ctx,
		retry.WithMaxRetries(maxStorageFailCount, retry.NewConstant(storageQueueRetryDelay)),
		func(ctx context.Context) error {
			peekItem, err := u.telemetryQueue.Peek()

			if err != nil {
				return retry.RetryableError(err)
			}

			item = peekItem
			return nil
		},
	)

	return item, err
}

func (u *TelemetryUploader) reliableRemove(ctx context.Context, item *StoredItem) error {
	return retry.Do(
		ctx,
		retry.WithMaxRetries(maxStorageFailCount, retry.NewConstant(storageQueueRetryDelay)),
		func(ctx context.Context) error {
			return retry.RetryableError(u.telemetryQueue.Remove(item))
		},
	)
}

// If repeated failures occur, enqueue will log but NOT return an error.
// With repeated failures, it means that the storage queue is in a bad state, such as disk being full.
// To avoid adding additional load, we do not want to enqueue the retry, and instead log the error and drop the message.
func (u *TelemetryUploader) enqueueRetry(
	ctx context.Context,
	itemName string,
	payload []byte,
	delayDuration time.Duration,
	attempts int,
) {
	err := retry.Do(
		ctx,
		retry.WithMaxRetries(maxStorageFailCount, retry.NewConstant(storageQueueRetryDelay)),
		func(ctx context.Context) error {
			return retry.RetryableError(u.telemetryQueue.EnqueueWithDelay(payload, delayDuration, attempts))
		},
	)

	if err != nil {

		log.Printf("unable to requeue item %v after %d attempts", itemName, attempts)
	}
}

func (u *TelemetryUploader) transmit(ctx context.Context, item *StoredItem) {
	payload := item.Message()
	var telemetryItems appinsightsexporter.TelemetryItems
	if u.isDebugMode {
		// When in debug mode, we deserialize to get better error messages
		telemetryItems.Deserialize(payload)
	}
	result, err := u.transmitter.Transmit(payload, telemetryItems)
	if err == nil && result != nil && result.IsSuccess() {
		return
	}

	attempts := item.RetryCount() + 1

	if err != nil || result == nil {
		if attempts > maxRetryCount {
			log.Printf("failed to send %v after %d attempts, err: %v\n", item.fileName, maxRetryCount, err)
			return
		}

		u.enqueueRetry(ctx, item.fileName, payload, defaultTransmitRetryDelay, attempts)
	} else if result.CanRetry() {
		if attempts > maxRetryCount {
			log.Printf("failed to send %v after %d attempts, statusCode: %d\n", item.fileName, maxRetryCount, result.StatusCode)
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
			u.enqueueRetry(ctx, item.fileName, newPayload, retryDelay, attempts)
		} else {
			u.enqueueRetry(ctx, item.fileName, payload, retryDelay, attempts)
		}
	} else {
		log.Printf("failed to transmit item %s with non-retriable status code %d\n", item.fileName, result.StatusCode)
	}
}

func (u *TelemetryUploader) calculateRetryDelay(retryAfter *time.Time, attempts int) time.Duration {
	if retryAfter != nil {
		return u.clock.Until(*retryAfter)
	} else if attempts == 1 {
		return firstTransmitRetryDelay
	} else {
		return defaultTransmitRetryDelay
	}
}

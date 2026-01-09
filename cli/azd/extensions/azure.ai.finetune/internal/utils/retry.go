// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"context"
	"time"

	"github.com/sethvargo/go-retry"
)

const (
	// DefaultMaxAttempts is the default number of retry attempts
	DefaultMaxAttempts = 3
	// DefaultDelaySeconds is the default initial delay in seconds
	DefaultDelaySeconds = 2
)

// DefaultRetryConfig returns a default exponential backoff strategy
func DefaultRetryConfig() retry.Backoff {
	return retry.WithMaxRetries(
		DefaultMaxAttempts-1,
		retry.NewExponential(DefaultDelaySeconds*time.Second),
	)
}

// RetryOperation executes the given operation with retry logic
// All errors returned by the operation are considered retryable
func RetryOperation(ctx context.Context, backoff retry.Backoff, operation func() error) error {
	if backoff == nil {
		backoff = DefaultRetryConfig()
	}

	return retry.Do(ctx, backoff, func(ctx context.Context) error {
		if err := operation(); err != nil {
			return retry.RetryableError(err)
		}
		return nil
	})
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"context"
	"fmt"
	"time"
)

const (
	// DefaultMaxAttempts is the default number of retry attempts
	DefaultMaxAttempts = 3
	// DefaultDelaySeconds is the default initial delay in seconds
	DefaultDelaySeconds = 2
)

// RetryConfig holds configuration for retry operations
type RetryConfig struct {
	MaxAttempts int
	Delay       time.Duration
	BackoffFunc func(attempt int, delay time.Duration) time.Duration
}

// DefaultRetryConfig returns a sensible default retry configuration
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxAttempts: DefaultMaxAttempts,
		Delay:       DefaultDelaySeconds * time.Second,
		BackoffFunc: func(attempt int, delay time.Duration) time.Duration {
			// Exponential backoff: 2s, 4s, 8s
			return delay * time.Duration(1<<(attempt-1))
		},
	}
}

// RetryOperation executes the given operation with retry logic
// The operation should return an error if it should be retried
func RetryOperation(ctx context.Context, config *RetryConfig, operation func() error) error {
	if config == nil {
		config = DefaultRetryConfig()
	}

	var lastErr error

	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		// Execute the operation
		err := operation()
		if err == nil {
			return nil // Success!
		}

		lastErr = err

		// If this was the last attempt, don't wait
		if attempt == config.MaxAttempts {
			break
		}

		// Calculate delay for this attempt
		delay := config.BackoffFunc(attempt, config.Delay)

		// Wait before retrying, respecting context cancellation
		select {
		case <-time.After(delay):
			// Continue to next attempt
		case <-ctx.Done():
			return fmt.Errorf("operation cancelled: %w", ctx.Err())
		}
	}

	return lastErr
}

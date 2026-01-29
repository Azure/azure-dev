// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultRetryConfig(t *testing.T) {
	config := DefaultRetryConfig()
	require.NotNil(t, config)
}

func TestRetryOperation_Success(t *testing.T) {
	ctx := context.Background()
	callCount := 0

	err := RetryOperation(ctx, DefaultRetryConfig(), func() error {
		callCount++
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, 1, callCount, "Operation should be called exactly once on success")
}

func TestRetryOperation_FailsThenSucceeds(t *testing.T) {
	ctx := context.Background()
	callCount := 0

	err := RetryOperation(ctx, DefaultRetryConfig(), func() error {
		callCount++
		if callCount < 2 {
			return errors.New("temporary error")
		}
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, 2, callCount, "Operation should be retried once")
}

func TestRetryOperation_AllAttemptsFail(t *testing.T) {
	ctx := context.Background()
	callCount := 0
	expectedError := errors.New("persistent error")

	err := RetryOperation(ctx, DefaultRetryConfig(), func() error {
		callCount++
		return expectedError
	})

	require.Error(t, err)
	require.Equal(t, DefaultMaxAttempts, callCount, "Should retry DefaultMaxAttempts times")
}

func TestRetryOperation_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	callCount := 0

	// Cancel immediately
	cancel()

	err := RetryOperation(ctx, DefaultRetryConfig(), func() error {
		callCount++
		return errors.New("should not reach here multiple times")
	})

	require.Error(t, err)
	// With immediate cancellation, it might execute once or zero times
	require.LessOrEqual(t, callCount, 1)
}

func TestRetryOperation_ContextAlreadyCancelled(t *testing.T) {
	// Test with already-cancelled context to avoid timing-dependent behavior
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before starting

	err := RetryOperation(ctx, DefaultRetryConfig(), func() error {
		return errors.New("should not retry with cancelled context")
	})

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

func TestRetryOperation_NilBackoff_UsesDefault(t *testing.T) {
	ctx := context.Background()
	callCount := 0

	err := RetryOperation(ctx, nil, func() error {
		callCount++
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, 1, callCount)
}

func TestRetryOperation_SucceedsOnLastAttempt(t *testing.T) {
	ctx := context.Background()
	callCount := 0

	err := RetryOperation(ctx, DefaultRetryConfig(), func() error {
		callCount++
		if callCount < DefaultMaxAttempts {
			return errors.New("retry needed")
		}
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, DefaultMaxAttempts, callCount)
}

func TestDefaultRetryConfig_Constants(t *testing.T) {
	require.Equal(t, 3, DefaultMaxAttempts)
	require.Equal(t, 2, DefaultDelaySeconds)
}

func TestRetryOperation_DifferentErrorTypes(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"SimpleError", errors.New("simple error")},
		{"WrappedError", errors.New("wrapped: inner error")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			callCount := 0

			err := RetryOperation(ctx, DefaultRetryConfig(), func() error {
				callCount++
				return tt.err
			})

			require.Error(t, err)
			require.Equal(t, DefaultMaxAttempts, callCount)
		})
	}
}

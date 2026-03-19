// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLazyRetryInit_SuccessCached(t *testing.T) {
	var l LazyRetryInit
	callCount := int32(0)

	for range 3 {
		err := l.Do(func() error {
			atomic.AddInt32(&callCount, 1)
			return nil
		})
		require.NoError(t, err)
	}

	require.Equal(t, int32(1), atomic.LoadInt32(&callCount), "f should only be called once on success")
}

func TestLazyRetryInit_FailureRetried(t *testing.T) {
	var l LazyRetryInit
	callCount := int32(0)
	errTemporary := errors.New("temporary failure")

	// First call fails
	err := l.Do(func() error {
		atomic.AddInt32(&callCount, 1)
		return errTemporary
	})
	require.ErrorIs(t, err, errTemporary)
	require.Equal(t, int32(1), atomic.LoadInt32(&callCount))

	// Second call retries and succeeds
	err = l.Do(func() error {
		atomic.AddInt32(&callCount, 1)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, int32(2), atomic.LoadInt32(&callCount))

	// Third call is cached
	err = l.Do(func() error {
		atomic.AddInt32(&callCount, 1)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, int32(2), atomic.LoadInt32(&callCount))
}

func TestLazyRetryInit_ConcurrentSuccess(t *testing.T) {
	var l LazyRetryInit
	var callCount int32

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			_ = l.Do(func() error {
				atomic.AddInt32(&callCount, 1)
				time.Sleep(10 * time.Millisecond)
				return nil
			})
		})
	}
	wg.Wait()

	// Only one goroutine should have executed the init function (since it succeeded)
	require.Equal(t, int32(1), atomic.LoadInt32(&callCount))
}

func TestLazyRetryInit_ConcurrentFailureRetry(t *testing.T) {
	var l LazyRetryInit
	var callCount int32
	errTemporary := errors.New("temporary failure")

	// First wave: all goroutines should see the failure
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			err := l.Do(func() error {
				atomic.AddInt32(&callCount, 1)
				time.Sleep(5 * time.Millisecond)
				return errTemporary
			})
			require.ErrorIs(t, err, errTemporary)
		})
	}
	wg.Wait()

	// At least one goroutine must have called the init function, but since the mutex
	// serializes access and each call fails (not cached), multiple calls are expected.
	firstWaveCount := atomic.LoadInt32(&callCount)
	require.GreaterOrEqual(t, firstWaveCount, int32(1))

	// Second wave: succeed this time
	atomic.StoreInt32(&callCount, 0)
	for range 20 {
		wg.Go(func() {
			_ = l.Do(func() error {
				atomic.AddInt32(&callCount, 1)
				return nil
			})
		})
	}
	wg.Wait()

	// After success, only one goroutine should execute the init (the first to acquire the lock).
	// Subsequent goroutines find done==true and skip.
	require.Equal(t, int32(1), atomic.LoadInt32(&callCount))
}

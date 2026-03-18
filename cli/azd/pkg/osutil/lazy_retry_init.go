// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
	"sync"
	"sync/atomic"
)

// LazyRetryInit provides a thread-safe initialization pattern that caches success
// but allows retries on failure. Unlike sync.Once, failed calls are not cached,
// so subsequent calls can retry the initialization.
// Uses atomic.Bool for a lock-free fast path after successful initialization.
type LazyRetryInit struct {
	mu   sync.Mutex
	done atomic.Bool
}

// Do calls f if initialization has not yet succeeded. If f returns nil,
// the result is cached and subsequent calls return nil immediately (lock-free fast path).
// If f returns an error, the error is returned and f will be called again
// on the next invocation.
func (l *LazyRetryInit) Do(f func() error) error {
	if l.done.Load() {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.done.Load() {
		return nil
	}

	if err := f(); err != nil {
		return err
	}

	l.done.Store(true)
	return nil
}

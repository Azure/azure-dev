// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import "sync"

// clientCache provides thread-safe caching of ARM SDK clients by a string key (typically subscription ID).
// ARM SDK clients are designed to be long-lived and reuse HTTP connections via their internal pipeline.
// Creating them per-call wastes TCP+TLS handshakes; caching eliminates this overhead.
type clientCache[T any] struct {
	cache sync.Map
}

// GetOrCreate returns a cached client for the given key, or creates one using the factory function.
// The factory is only called on cache miss. Thread-safe via sync.Map.LoadOrStore.
func (c *clientCache[T]) GetOrCreate(key string, factory func() (T, error)) (T, error) {
	if cached, ok := c.cache.Load(key); ok {
		return cached.(T), nil
	}

	client, err := factory()
	if err != nil {
		var zero T
		return zero, err
	}

	actual, _ := c.cache.LoadOrStore(key, client)
	return actual.(T), nil
}

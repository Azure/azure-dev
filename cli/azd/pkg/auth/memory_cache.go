// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"bytes"
	"sync"
)

type fixedMarshaller struct {
	val []byte
}

func (f *fixedMarshaller) Marshal() ([]byte, error) {
	return f.val, nil
}

func (f *fixedMarshaller) Unmarshal(cache []byte) error {
	f.val = cache
	return nil
}

// memoryCache is a simple memory cache that implements Cache. During export, if the cache contents has not changed, the
// inner cache is not notified of a change.
type memoryCache struct {
	mu    sync.RWMutex
	cache map[string][]byte
	inner Cache
}

func (c *memoryCache) Read(key string) ([]byte, error) {
	c.mu.RLock()
	v, has := c.cache[key]
	c.mu.RUnlock()

	if has {
		return v, nil
	}

	if c.inner != nil {
		return c.inner.Read(key)
	}

	return nil, errCacheKeyNotFound
}

func (c *memoryCache) Set(key string, value []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if bytes.Equal(c.cache[key], value) {
		// no change, nothing more to do.
		return nil
	}

	if c.inner != nil {
		if err := c.inner.Set(key, value); err != nil {
			return err
		}
	}

	c.cache[key] = value
	return nil
}

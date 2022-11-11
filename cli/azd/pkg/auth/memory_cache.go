// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"bytes"
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
	cache map[string][]byte
	inner Cache
}

func (c *memoryCache) Read(key string) ([]byte, error) {
	if v, has := c.cache[key]; has {
		return v, nil
	} else if c.inner != nil {
		return c.inner.Read(key)
	}

	return nil, nil
}

func (c *memoryCache) Set(key string, value []byte) error {
	old := c.cache[key]

	if bytes.Equal(old, value) {
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

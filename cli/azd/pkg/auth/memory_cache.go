// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"bytes"
	"fmt"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
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

// memoryCache is a simple memory cache that implements exportReplaceWithErrors. During export, if the cache
// contents has not changed, the nested cache is not notified of a change.
type memoryCache struct {
	cache map[string][]byte
	inner exportReplaceWithErrors
}

// cacheUpdatingUnmarshaler implements cache.Unmarshaler. During unmarshalling it updates the value in the memory
// cache and then forwards the call to the next unmarshaler (which will typically update MSAL's internal cache).
type cacheUpdatingUnmarshaler struct {
	c     *memoryCache
	key   string
	inner cache.Unmarshaler
}

func (r *cacheUpdatingUnmarshaler) Unmarshal(b []byte) error {
	r.c.cache[r.key] = b
	return r.inner.Unmarshal(b)
}

func (c *memoryCache) Replace(cache cache.Unmarshaler, key string) error {
	if v, has := c.cache[key]; has {
		if err := cache.Unmarshal(v); err != nil {
			return fmt.Errorf("failed to unmarshal value into cache: %w", err)
		}
	} else if c.inner != nil {
		return c.inner.Replace(&cacheUpdatingUnmarshaler{
			c:     c,
			key:   key,
			inner: cache,
		}, key)
	}

	return nil
}

func (c *memoryCache) Export(cache cache.Marshaler, key string) error {
	new, err := cache.Marshal()
	if err != nil {
		return fmt.Errorf("error marshaling existing msal cache: %w", err)
	}

	old := c.cache[key]

	if bytes.Equal(old, new) {
		// no change, nothing more to do.
		return nil
	}

	c.cache[key] = new
	if c.inner != nil {
		return c.inner.Export(cache, key)
	}

	return nil
}

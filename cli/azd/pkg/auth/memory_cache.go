// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"bytes"
	"log"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
)

var _ cache.ExportReplace = &memoryCache{}

var _ cache.Marshaler = &fixedMarshaller{}
var _ cache.Unmarshaler = &fixedMarshaller{}

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

// memoryCache is a simple memory cache that implements cache.ExportReplace. During export, if the cache
// contents has not changed, the nested cache is not notified of a change.
type memoryCache struct {
	cache map[string][]byte
	inner cache.ExportReplace
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

func (c *memoryCache) Replace(cache cache.Unmarshaler, key string) {
	if v, has := c.cache[key]; has {
		if err := cache.Unmarshal(v); err != nil {
			log.Printf("failed to unmarshal value into cache: %v", err)
		}
	} else if c.inner != nil {
		c.inner.Replace(&cacheUpdatingUnmarshaler{
			c:     c,
			key:   key,
			inner: cache,
		}, key)
	} else {
		log.Printf("no existing cache entry found with key '%s'", key)
	}
}

func (c *memoryCache) Export(cache cache.Marshaler, key string) {
	new, err := cache.Marshal()
	if err != nil {
		log.Printf("error marshaling existing msal cache: %v", err)
		return
	}

	old := c.cache[key]

	if bytes.Equal(old, new) {
		// no change, nothing more to do.
		return
	}

	c.cache[key] = new
	c.inner.Export(cache, key)
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"log"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
)

// msalCacheAdapter adapts our interface to the one expected by cache.ExportReplace. Since that
// interface is not error returning, any errors during Export or Replace are simply logged for
// debugging purposes.
type msalCacheAdapter struct {
	cache Cache
}

func (a *msalCacheAdapter) Replace(cache cache.Unmarshaler, key string) {
	val, err := a.cache.Read(key)
	if err != nil {
		log.Printf("ignoring error in adapter: %v", err)
	}

	if err := cache.Unmarshal(val); err != nil {
		log.Printf("ignoring error in adapter: %v", err)
	}
}

func (a *msalCacheAdapter) Export(cache cache.Marshaler, key string) {
	val, err := cache.Marshal()
	if err != nil {
		log.Printf("ignoring error in adapter: %v", err)
	}

	if err := a.cache.Set(key, val); err != nil {
		log.Printf("ignoring error in adapter: %v", err)
	}
}

type Cache interface {
	Read(key string) ([]byte, error)
	Set(key string, value []byte) error
}

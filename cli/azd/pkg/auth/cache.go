// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"log"
	"runtime"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
)

// msalCacheAdapter adapts our interface to the one expected by cache.ExportReplace. Since that
// interface is not error returning, any errors during Export or Replace are simply logged for
// debugging purposes.
type msalCacheAdapter struct {
	cache Cache
}

func (a *msalCacheAdapter) Replace(cache cache.Unmarshaler, key string) {
	pc, _, _, ok := runtime.Caller(1)
	details := runtime.FuncForPC(pc)
	if ok && details != nil {
		log.Printf("Replace(%s): called from %s\n", key, details.Name())
	}

	val, err := a.cache.Read(key)
	if err != nil {
		log.Printf("ignoring error in adapter: %v", err)
	}

	if err := cache.Unmarshal(val); err != nil {
		log.Printf("ignoring error in adapter: %v", err)
	}
}

func (a *msalCacheAdapter) Export(cache cache.Marshaler, key string) {
	pc, _, _, ok := runtime.Caller(1)
	details := runtime.FuncForPC(pc)
	if ok && details != nil {
		log.Printf("Export(%s): called from %s\n", key, details.Name())
	}

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

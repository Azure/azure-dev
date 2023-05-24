// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"errors"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
)

// The MSAL cache key for the current user.
// For historical purposes, this is an empty string.
const cCurrentUserCacheKey = ""

// msalCacheAdapter adapts our interface to the one expected by cache.ExportReplace.
type msalCacheAdapter struct {
	cache    Cache
	fixedKey *string
}

func (a *msalCacheAdapter) key(suggestedKey string) string {
	if a.fixedKey != nil {
		return *a.fixedKey
	}

	return suggestedKey
}

func (a *msalCacheAdapter) Replace(ctx context.Context, cache cache.Unmarshaler, cacheHints cache.ReplaceHints) error {
	key := a.key(cacheHints.PartitionKey)
	val, err := a.cache.Read(key)
	if errors.Is(err, errCacheKeyNotFound) {
		return nil
	} else if err != nil {
		return err
	}

	// Replace the msal cache contents with the new value retrieved.
	if err := cache.Unmarshal(val); err != nil {
		return err
	}
	return nil
}

func (a *msalCacheAdapter) Export(ctx context.Context, cache cache.Marshaler, cacheHints cache.ExportHints) error {
	key := a.key(cacheHints.PartitionKey)
	val, err := cache.Marshal()
	if err != nil {
		return err
	}

	return a.cache.Set(key, val)
}

type Cache interface {
	Read(key string) ([]byte, error)
	Set(key string, value []byte) error
}

var errCacheKeyNotFound = errors.New("key not found")

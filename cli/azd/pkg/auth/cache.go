// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"errors"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
)

// The MSAL cache key for the current user. The stored MSAL cached data contains
// all accounts with stored credentials, across all tenants.
// Currently, the underlying MSAL cache data is represented as [Contract] inside the library.
//
// For simplicity in naming the final cached file, which has a unique directory (see [fileCache]),
// and for historical purposes, we use empty string as the key.
//
// It may be tempting to instead use the partition key provided by [cache.ReplaceHints],
// but note that the key is a partitioning key and not a unique user key.
// Also, given that the data contains auth data for all users, we only need a single key
// to store all cached auth information.
const cCurrentUserCacheKey = ""

// msalCacheAdapter adapts our interface to the one expected by cache.ExportReplace.
type msalCacheAdapter struct {
	cache Cache
}

func (a *msalCacheAdapter) Replace(ctx context.Context, cache cache.Unmarshaler, _ cache.ReplaceHints) error {
	val, err := a.cache.Read(cCurrentUserCacheKey)
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

func (a *msalCacheAdapter) Export(ctx context.Context, cache cache.Marshaler, _ cache.ExportHints) error {
	val, err := cache.Marshal()
	if err != nil {
		return err
	}

	return a.cache.Set(cCurrentUserCacheKey, val)
}

type Cache interface {
	Read(key string) ([]byte, error)
	Set(key string, value []byte) error
}

var errCacheKeyNotFound = errors.New("key not found")

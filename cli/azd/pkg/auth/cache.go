// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
)

// msalCacheAdapter adapts our interface to the one expected by cache.ExportReplace. Since that
// interface is not error returning, any errors during Export or Replace are simply logged for
// debugging purposes.
type msalCacheAdapter struct {
	cache Cache
}

func (a *msalCacheAdapter) Replace(ctx context.Context, cache cache.Unmarshaler, cacheHints cache.ReplaceHints) error {
	val, err := a.cache.Read(cacheHints.PartitionKey)
	if err != nil {
		return err
	}

	if err := cache.Unmarshal(val); err != nil {
		return err
	}
	return nil
}

func (a *msalCacheAdapter) Export(ctx context.Context, cache cache.Marshaler, cacheHints cache.ExportHints) error {
	val, err := cache.Marshal()
	if err != nil {
		return err
	}

	err = a.cache.Set(cacheHints.PartitionKey, val)
	return err
}

type Cache interface {
	Read(key string) ([]byte, error)
	Set(key string, value []byte) error
}

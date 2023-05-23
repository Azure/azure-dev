// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build unix
// +build unix

package auth

import (
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
)

// newCache creates a cache implementation that satisfies [cache.ExportReplace] from the MSAL library,
// returning both the [cache.ExportReplace] adapter and the underlying [Cache].
//
// root must be created beforehand, and must point to a directory.
func newCache(root string) (cache.ExportReplace, Cache) {
	adapter := &msalCacheAdapter{
		cache: &memoryCache{
			cache: make(map[string][]byte),
			inner: &fileCache{
				prefix: "cache",
				root:   root,
				ext:    "json",
			},
		},
	}

	return adapter, adapter.cache
}

// newCredentialCache creates a cache implementation for storing credentials.
//
// root must be created beforehand, and must point to a directory.
func newCredentialCache(root string) Cache {
	return &memoryCache{
		cache: make(map[string][]byte),
		inner: &fileCache{
			prefix: "cred",
			root:   root,
			ext:    "json",
		},
	}
}

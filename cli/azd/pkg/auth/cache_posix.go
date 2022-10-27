// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build unix
// +build unix

package auth

import (
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
)

func newCache(root string) cache.ExportReplace {
	return &memoryCache{
		cache: make(map[string][]byte),
		inner: &fileCache{
			root: root,
		},
	}
}

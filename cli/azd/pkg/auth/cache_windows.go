// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows
// +build windows

package auth

import (
	"fmt"
	"unsafe"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"golang.org/x/sys/windows"
)

func newCache(root string) cache.ExportReplace {
	return &msalCacheAdapter{
		cache: &memoryCache{
			cache: make(map[string][]byte),
			inner: &encryptedCache{
				inner: &fileCache{
					prefix: "cache",
					root:   root,
					ext:    "bin",
				},
			},
		},
	}
}

func newCredentialCache(root string) Cache {
	return &memoryCache{
		cache: make(map[string][]byte),
		inner: &encryptedCache{
			inner: &fileCache{
				prefix: "cred",
				root:   root,
				ext:    "bin",
			},
		},
	}
}

// encryptedCache is a Cache that wraps an existing Cache, encrypting and decrypting the cached value with CryptProtectData
type encryptedCache struct {
	inner Cache
}

func (c *encryptedCache) Read(key string) ([]byte, error) {
	val, err := c.inner.Read(key)
	if err != nil {
		return nil, err
	}

	if len(val) == 0 {
		return val, nil
	}

	encryptedBlob := windows.DataBlob{
		Size: uint32(len(val)),
		Data: &val[0],
	}

	var plaintext windows.DataBlob

	if err := windows.CryptUnprotectData(&encryptedBlob, nil, nil, uintptr(0), nil, 0, &plaintext); err != nil {
		return nil, fmt.Errorf("failed to decrypt data: %w", err)
	}

	decryptedSlice := unsafe.Slice(plaintext.Data, plaintext.Size)

	cs := make([]byte, plaintext.Size)
	copy(cs, decryptedSlice)

	if _, err := windows.LocalFree(windows.Handle(unsafe.Pointer(plaintext.Data))); err != nil {
		return nil, fmt.Errorf("failed to free encrypted data: %w", err)
	}

	return cs, nil
}

func (c *encryptedCache) Set(key string, val []byte) error {
	if len(val) == 0 {
		return c.inner.Set(key, val)
	}

	plaintext := windows.DataBlob{
		Size: uint32(len(val)),
		Data: &val[0],
	}
	var encrypted windows.DataBlob

	if err := windows.CryptProtectData(&plaintext, nil, nil, uintptr(0), nil, 0, &encrypted); err != nil {
		return fmt.Errorf("failed to encrypt data: %w", err)
	}

	encryptedSlice := unsafe.Slice(encrypted.Data, encrypted.Size)

	cs := make([]byte, encrypted.Size)
	copy(cs, encryptedSlice)

	if _, err := windows.LocalFree(windows.Handle(unsafe.Pointer(encrypted.Data))); err != nil {
		return fmt.Errorf("failed to free encrypted data: %w", err)
	}

	return c.inner.Set(key, cs)
}

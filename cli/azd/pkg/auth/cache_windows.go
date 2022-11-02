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
	return &errorDroppingCacheAdapter{
		inner: &memoryCache{
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

func newCredentialCache(root string) exportReplaceWithErrors {
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

// encryptedCache is a cache.ExportReplace that wraps an existing ExportReplaceWithErrors, encrypting and decrypting the
// cached value with CryptProtectData
type encryptedCache struct {
	inner exportReplaceWithErrors
}

func (c encryptedCache) Export(cache cache.Marshaler, key string) error {
	res, err := cache.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal cache: %w", err)
	}

	if len(res) == 0 {
		return c.inner.Export(&fixedMarshaller{
			val: []byte{},
		}, key)
	}

	plaintext := windows.DataBlob{
		Size: uint32(len(res)),
		Data: &res[0],
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

	return c.inner.Export(&fixedMarshaller{
		val: []byte(cs),
	}, key)
}

func (c *encryptedCache) Replace(cache cache.Unmarshaler, key string) error {
	capture := &fixedMarshaller{}
	if err := c.inner.Replace(capture, key); err != nil {
		return err
	}

	if len(capture.val) == 0 {
		if err := cache.Unmarshal([]byte{}); err != nil {
			return fmt.Errorf("failed to unmarshal decrypted cache: %w", err)
		}
	}

	encrypted := windows.DataBlob{
		Size: uint32(len(capture.val)),
		Data: &capture.val[0],
	}

	var plaintext windows.DataBlob

	if err := windows.CryptUnprotectData(&encrypted, nil, nil, uintptr(0), nil, 0, &plaintext); err != nil {
		return fmt.Errorf("failed to decrypt data: %v", err)
	}

	decryptedSlice := unsafe.Slice(plaintext.Data, plaintext.Size)

	cs := make([]byte, plaintext.Size)
	copy(cs, decryptedSlice)

	if _, err := windows.LocalFree(windows.Handle(unsafe.Pointer(plaintext.Data))); err != nil {
		return fmt.Errorf("failed to free encrypted data: %v", err)
	}

	return cache.Unmarshal(cs)
}

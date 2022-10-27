// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows
// +build windows

package auth

import (
	"fmt"
	"log"
	"unsafe"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"golang.org/x/sys/windows"
)

func newCache(root string) cache.ExportReplace {
	return &memoryCache{
		cache: make(map[string][]byte),
		inner: &encryptedCache{
			inner: &fileCache{
				root: root,
			},
		},
	}
}

var _ cache.ExportReplace = &encryptedCache{}

// encryptedCache is a cache.ExportReplace that wraps an existing cache.ExportReplace, encrypting and decrypting the
// cached value with CryptProtectData
type encryptedCache struct {
	inner cache.ExportReplace
}

func (c *encryptedCache) Export(cache cache.Marshaler, key string) {
	log.Printf("encryptedCache: exporting cache with key '%s'", key)
	res, err := cache.Marshal()
	if err != nil {
		fmt.Printf("failed to marshal cache from MSAL: %v", err)
		return
	}

	plaintext := windows.DataBlob{
		Size: uint32(len(res)),
		// TODO(ellismg): pinning?
		Data: &res[0],
	}
	var encrypted windows.DataBlob

	if err := windows.CryptProtectData(&plaintext, nil, nil, uintptr(0), nil, 0, &encrypted); err != nil {
		fmt.Printf("failed to encrypt data: %v", err)
	}

	encryptedSlice := unsafe.Slice(encrypted.Data, encrypted.Size)

	cs := make([]byte, encrypted.Size)
	for i := uint32(0); i < encrypted.Size; i++ {
		cs[i] = encryptedSlice[i]
	}

	if _, err := windows.LocalFree(windows.Handle(unsafe.Pointer(encrypted.Data))); err != nil {
		log.Printf("failed to free encrypted data: %v", err)
	}

	c.inner.Export(&fixedMarshaller{
		val: []byte(cs),
	}, key)
}

func (c *encryptedCache) Replace(cache cache.Unmarshaler, key string) {
	log.Printf("encryptedCache: replacing cache with key '%s'", key)

	capture := &fixedMarshaller{}
	c.inner.Replace(capture, key)

	if len(capture.val) == 0 {
		log.Printf("encrypted cache is empty, ignoring")
		return
	}

	encrypted := windows.DataBlob{
		Size: uint32(len(capture.val)),
		// TODO(ellismg): pinning?
		Data: &capture.val[0],
	}

	var plaintext windows.DataBlob

	if err := windows.CryptUnprotectData(&encrypted, nil, nil, uintptr(0), nil, 0, &plaintext); err != nil {
		fmt.Printf("failed to decrypt data: %v", err)
	}

	decryptedSlice := unsafe.Slice(plaintext.Data, plaintext.Size)

	cs := make([]byte, plaintext.Size)
	for i := uint32(0); i < plaintext.Size; i++ {
		cs[i] = decryptedSlice[i]
	}

	if _, err := windows.LocalFree(windows.Handle(unsafe.Pointer(plaintext.Data))); err != nil {
		log.Printf("failed to free encrypted data: %v", err)
	}
}

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
				prefix: "cache",
				root:   root,
				ext:    "bin",
			},
		},
	}
}

func newCredentialCache(root string) cache.ExportReplace {
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

var _ cache.ExportReplace = &encryptedCache{}

// encryptedCache is a cache.ExportReplace that wraps an existing cache.ExportReplace, encrypting and decrypting the
// cached value with CryptProtectData
type encryptedCache struct {
	inner cache.ExportReplace
}

func (c *encryptedCache) Export(cache cache.Marshaler, key string) {
	res, err := cache.Marshal()
	if err != nil {
		fmt.Printf("failed to marshal cache from MSAL: %v", err)
		return
	}

	if len(res) == 0 {
		c.inner.Export(&fixedMarshaller{
			val: []byte{},
		}, key)

		return
	}

	plaintext := windows.DataBlob{
		Size: uint32(len(res)),
		Data: &res[0],
	}
	var encrypted windows.DataBlob

	if err := windows.CryptProtectData(&plaintext, nil, nil, uintptr(0), nil, 0, &encrypted); err != nil {
		fmt.Printf("failed to encrypt data: %v", err)
	}

	encryptedSlice := unsafe.Slice(encrypted.Data, encrypted.Size)

	cs := make([]byte, encrypted.Size)
	copy(cs, encryptedSlice)

	if _, err := windows.LocalFree(windows.Handle(unsafe.Pointer(encrypted.Data))); err != nil {
		log.Printf("failed to free encrypted data: %v", err)
	}

	c.inner.Export(&fixedMarshaller{
		val: []byte(cs),
	}, key)
}

func (c *encryptedCache) Replace(cache cache.Unmarshaler, key string) {
	capture := &fixedMarshaller{}
	c.inner.Replace(capture, key)

	if len(capture.val) == 0 {
		if err := cache.Unmarshal([]byte{}); err != nil {
			log.Printf("failed to unmarshal decrypted cache to msal: %v", err)
		}
		return
	}

	encrypted := windows.DataBlob{
		Size: uint32(len(capture.val)),
		Data: &capture.val[0],
	}

	var plaintext windows.DataBlob

	if err := windows.CryptUnprotectData(&encrypted, nil, nil, uintptr(0), nil, 0, &plaintext); err != nil {
		fmt.Printf("failed to decrypt data: %v", err)
	}

	decryptedSlice := unsafe.Slice(plaintext.Data, plaintext.Size)

	cs := make([]byte, plaintext.Size)
	copy(cs, decryptedSlice)

	if _, err := windows.LocalFree(windows.Handle(unsafe.Pointer(plaintext.Data))); err != nil {
		log.Printf("failed to free encrypted data: %v", err)
	}

	if err := cache.Unmarshal(cs); err != nil {
		log.Printf("failed to unmarshal decrypted cache to msal: %v", err)
	}
}

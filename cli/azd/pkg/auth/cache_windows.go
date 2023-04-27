// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows
// +build windows

package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"unsafe"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"golang.org/x/sys/windows"
)

// envelopedData stores both the type of encryption used as well as the encrypted data (as a base64 encoded string),
// allowing us to change the underlying encryption algorithm as needed (and then understand what we need to do decrypt)
type envelopedData struct {
	// The type of encryption that was used to store data.
	Type encryptionType `json:"type"`
	// The encrypted data, represented as a Base64 encoded string (using base64.StdEncoding)
	Data string `json:"data"`
}

type encryptionType string

// cCryptProtectDataEncryptionType is the encryption type that uses CryptProtectData/CryptUnprotectData for
// encryption and decryption.  See https://learn.microsoft.com/windows/win32/api/dpapi/nf-dpapi-cryptprotectdata
// for more information on these APIs.
const cCryptProtectDataEncryptionType encryptionType = "CryptProtectData"

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

	var encryptedBlob windows.DataBlob
	var data envelopedData

	if err := json.Unmarshal(val, &data); err != nil {
		// early versions of `azd` did not write the encrypted data in an envelope and instead just persisted
		// the result from CryptProtectData directly. If we fail to unmarshal the persisted data into the enveloped
		// structure, treat it as if the data was directly stored. The next call to Set will transparently upgrade
		// to the new format.

		encryptedBlob = windows.DataBlob{
			Size: uint32(len(val)),
			Data: &val[0],
		}
	} else {

		if data.Type != cCryptProtectDataEncryptionType {
			return nil, fmt.Errorf("unsupported encryption type: %s", data.Type)
		}

		data, err := base64.StdEncoding.DecodeString(data.Data)
		if err != nil {
			return nil, fmt.Errorf("decoding base64 data: %w", err)
		}

		encryptedBlob = windows.DataBlob{
			Size: uint32(len(data)),
			Data: &data[0],
		}
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

	toStore, err := json.Marshal(envelopedData{
		Type: cCryptProtectDataEncryptionType,
		Data: base64.StdEncoding.EncodeToString(cs),
	})

	// We never expect the above to fail.
	if err != nil {
		panic(fmt.Sprintf("failed to marshal enveloped data: %s", err))
	}

	return c.inner.Set(key, toStore)
}

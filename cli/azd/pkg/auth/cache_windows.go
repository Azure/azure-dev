//go:build windows
// +build windows

package auth

import (
	"fmt"

	"github.com/99designs/keyring"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
)

var azdKeyringAllowedBackends = []keyring.BackendType{keyring.WinCredBackend}

func newCache(root string) (cache.ExportReplace, error) {
	key, err := getCacheKey()
	if err != nil {
		return nil, fmt.Errorf("getting encryption key: %w", err)
	}

	return &memoryCache{
		cache: make(map[string][]byte),
		inner: &encryptedCache{
			key: key,
			inner: &fileCache{
				root: root,
			},
		},
	}, nil
}

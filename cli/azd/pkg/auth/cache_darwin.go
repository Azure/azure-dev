//go:build darwin
// +build darwin

package auth

import (
	"github.com/99designs/keyring"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
)

var azdKeyringAllowedBackends = []keyring.BackendType{keyring.KeychainBackend}

func newCache(root string) (cache.ExportReplace, error) {
	return &memoryCache{
		cache: make(map[string][]byte),
		inner: &fileCache{
			root: root,
		},
	}, nil
}

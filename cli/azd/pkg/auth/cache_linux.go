//go:build linux
// +build linux

package auth

import "github.com/99designs/keyring"

var azdKeyringAllowedBackends = []keyring.BackendType{keyring.SecretServiceBackend, keyring.KeyCtlBackend}

func newCache(root string) (cache.ExportReplace, error) {
	return &memoryCache{
		cache: make(map[string][]byte),
		inner: &fileCache{
			root: root,
		},
	}, nil
}
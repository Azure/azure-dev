//go:build darwin
// +build darwin

package auth

import "github.com/99designs/keyring"

var azdKeyringAllowedBackends = []keyring.BackendType{keyring.KeychainBackend}

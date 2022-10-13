//go:build linux
// +build linux

package auth

import "github.com/99designs/keyring"

var azdKeyringAllowedBackends = []keyring.BackendType{keyring.SecretServiceBackend}

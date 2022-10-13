//go:build windows
// +build windows

package auth

import "github.com/99designs/keyring"

var azdKeyringAllowedBackends = []keyring.BackendType{keyring.WinCredBackend}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package cmd

import (
	"errors"
	"syscall"
)

// wsaeConnRefused is the Windows Sockets "connection refused" error code
// (WSAECONNREFUSED, 10061). The standard syscall package does not export a
// named constant for it, so it is defined here.
const wsaeConnRefused = syscall.Errno(10061)

// isPlatformConnRefused matches the Windows Sockets connection-refused error
// (WSAECONNREFUSED). On Windows a refused dial surfaces as WSAECONNREFUSED
// rather than the POSIX syscall.ECONNREFUSED, so an errors.Is against the
// POSIX constant alone does not detect it.
func isPlatformConnRefused(err error) bool {
	return errors.Is(err, wsaeConnRefused)
}

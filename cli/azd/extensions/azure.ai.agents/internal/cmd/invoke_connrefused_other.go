// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package cmd

// isPlatformConnRefused is a no-op on non-Windows platforms; the POSIX
// syscall.ECONNREFUSED check in isConnectionRefused already covers them.
func isPlatformConnRefused(err error) bool {
	return false
}

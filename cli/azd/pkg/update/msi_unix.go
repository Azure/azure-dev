// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package update

import "syscall"

// newDetachedSysProcAttr is a no-op on non-Windows platforms.
// updateViaMSI is only called on Windows (guarded by runtime.GOOS check in Update).
func newDetachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

// msiLogFilePath is a no-op on non-Windows platforms.
func msiLogFilePath() (string, error) {
	return "", nil
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package internal

import "os"

// disableVirtualTerminalInput is a no-op on non-Windows platforms.
// The ENABLE_VIRTUAL_TERMINAL_INPUT console flag only exists on Windows.
func disableVirtualTerminalInput(_ *os.File) error {
	return nil
}

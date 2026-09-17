// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package internal

// disableVirtualTerminalInput is a no-op on non-Windows platforms.
func disableVirtualTerminalInput(_ FileReader) error {
	return nil
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package internal

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	getConsoleMode = kernel32.NewProc("GetConsoleMode")
	setConsoleMode = kernel32.NewProc("SetConsoleMode")
)

const enableVirtualTerminalInput uint32 = 0x0200

// disableVirtualTerminalInput clears the ENABLE_VIRTUAL_TERMINAL_INPUT flag
// on the console input handle. When this flag is set (e.g. by PowerShell),
// Windows translates arrow key presses into ANSI escape sequences instead of
// delivering them as native virtual key codes. The survey library's Windows
// ReadRune only handles native VK codes, so leaving VTI enabled causes
// escape sequence fragments to leak through as individual runes.
//
// This is called after survey's SetTermMode (which saves/restores the original
// console state), so RestoreTermMode will re-enable VTI if it was originally set.
func disableVirtualTerminalInput(f *os.File) error {
	handle := f.Fd()

	var mode uint32
	//nolint:gosec // Win32 API requires unsafe pointer
	r, _, err := getConsoleMode.Call(uintptr(handle), uintptr(unsafe.Pointer(&mode)))
	if r == 0 {
		return err
	}

	if mode&enableVirtualTerminalInput == 0 {
		return nil // VTI not enabled, nothing to do
	}

	newMode := mode &^ enableVirtualTerminalInput
	r, _, err = setConsoleMode.Call(uintptr(handle), uintptr(newMode))
	if r == 0 {
		return err
	}

	return nil
}

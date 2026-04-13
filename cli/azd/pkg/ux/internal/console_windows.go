// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package internal

import (
	"os"

	"golang.org/x/sys/windows"
)

const enableVirtualTerminalInput uint32 = 0x0200

// disableVirtualTerminalInput clears the ENABLE_VIRTUAL_TERMINAL_INPUT
// console mode flag so that Windows delivers native virtual-key codes
// (KEY_EVENT_RECORD with wVirtualKeyCode) instead of ANSI escape
// sequences for arrow keys and other special keys.
//
// The survey library's Windows RuneReader expects native VK codes
// (it checks unicodeChar == 0, then switches on wVirtualKeyCode).
// When ENABLE_VIRTUAL_TERMINAL_INPUT is set — as it is by default in
// Windows Terminal, PowerShell 7, VS Code, and Ghostty — the console
// emits ESC [ A / ESC [ B / etc. instead, which leak through as
// literal characters.
//
// This function is called after SetTermMode() and its effect is
// reversed when RestoreTermMode() restores the original console mode.
func disableVirtualTerminalInput(f *os.File) error {
	h := windows.Handle(f.Fd())

	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return err
	}

	if mode&enableVirtualTerminalInput == 0 {
		return nil // VTI not set, nothing to do
	}

	return windows.SetConsoleMode(h, mode&^enableVirtualTerminalInput)
}

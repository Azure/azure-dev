// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package terminal

import (
	"os"
	"strconv"

	"github.com/azure/azure-dev/cli/azd/internal/tracing/resource"
	"github.com/mattn/go-isatty"
)

// IsAttached returns true if the given file descriptors are attached to terminals.
// It reports the physical descriptor state without applying rendering policy overrides.
func IsAttached(stdoutFd uintptr, stdinFd uintptr) bool {
	return isAttached(stdoutFd, stdinFd, isatty.IsTerminal)
}

// IsTerminal returns true if the given file descriptors are attached to a terminal,
// taking into account of environment variables that force TTY behavior.
func IsTerminal(stdoutFd uintptr, stdinFd uintptr) bool {
	return isTerminal(stdoutFd, stdinFd, isatty.IsTerminal)
}

func isAttached(stdoutFd uintptr, stdinFd uintptr, isTerminalFileDescriptor func(uintptr) bool) bool {
	return isTerminalFileDescriptor(stdoutFd) && isTerminalFileDescriptor(stdinFd)
}

func isTerminal(stdoutFd uintptr, stdinFd uintptr, isTerminalFileDescriptor func(uintptr) bool) bool {
	// User override to force TTY behavior
	if forceTty, err := strconv.ParseBool(os.Getenv("AZD_FORCE_TTY")); err == nil {
		return forceTty
	}

	// By default, detect if we are running on CI and force no TTY mode if we are.
	// If this is affecting you locally while debugging on a CI machine,
	// use the override AZD_FORCE_TTY=true.
	if resource.IsRunningOnCI() {
		return false
	}

	// Agent markers are not considered here because child terminals inherit them.
	return isAttached(stdoutFd, stdinFd, isTerminalFileDescriptor)
}

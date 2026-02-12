// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package terminal

import (
	"os"
	"strconv"

	"github.com/azure/azure-dev/cli/azd/internal/runcontext/agentdetect"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/resource"
	"github.com/mattn/go-isatty"
)

// IsTerminal returns true if the given file descriptors are attached to a terminal,
// taking into account of environment variables that force TTY behavior.
func IsTerminal(stdoutFd uintptr, stdinFd uintptr) bool {
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

	// If running under an AI coding agent, disable TTY mode to prevent interactive prompts.
	if agentdetect.IsRunningInAgent() {
		return false
	}

	return isatty.IsTerminal(stdoutFd) && isatty.IsTerminal(stdinFd)
}

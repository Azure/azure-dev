// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package processutil

import (
	"errors"
	"os"
	"syscall"
)

// signalGraceful asks the process to shut down with SIGTERM, giving it a chance to run
// its own cleanup before Terminate escalates.
func signalGraceful(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	return process.Signal(syscall.SIGTERM)
}

// forceKill stops the process immediately with SIGKILL.
//
// scope is accepted for parity with the Windows implementation, which pins the process
// identity with an open handle so its containment check cannot be invalidated before the
// kill. Unix has no portable equivalent: Linux could use a pidfd, macOS offers nothing of
// the sort, so here the caller's re-check immediately before this call narrows the window
// rather than closing it.
func forceKill(pid int, _ string) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	return process.Signal(syscall.SIGKILL)
}

// processRunning reports whether the process exists. Signal 0 performs the existence
// check without delivering anything. EPERM means the process exists but is owned by
// another user, which still counts as running.
func processRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	err = process.Signal(syscall.Signal(0))

	return err == nil || errors.Is(err, syscall.EPERM)
}

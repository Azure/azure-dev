// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package processutil

import (
	"context"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code Windows reports for a process that has not exited.
const stillActive = 259

// signalGraceful is intentionally unsupported on Windows.
//
// Windows has no safe general purpose graceful stop for a console-less process that azd
// did not create. GenerateConsoleCtrlEvent targets a process group rather than a single
// process, so it can reach azd itself whenever the target shares azd's console. Killing
// the CLI to avoid a hard kill of an extension is not a trade worth making, so callers
// escalate straight to forceKill. Returning an error keeps Terminate's control flow
// identical across platforms.
func signalGraceful(pid int) error {
	return fmt.Errorf("graceful stop is not supported on windows for process %d", pid)
}

// forceKill stops a process immediately using TerminateProcess, refusing to act when the
// process behind pid is no longer the one inside scope.
//
// The open handle is what makes the scope check binding rather than merely probable.
// Windows keeps a process id reserved for as long as any handle to that process object is
// open, so the identity established by the image path query below cannot be swapped for
// an unrelated process before TerminateProcess runs. Validating a bare pid and then
// reopening it to kill could not offer that: the process may exit and the operating
// system hand the same number to something else in between.
func forceKill(pid int, scope string) error {
	//nolint:gosec // G115: pid is validated as positive by Terminate.
	handle, err := windows.OpenProcess(
		windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(pid),
	)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)

	executable := imagePathForHandle(handle)
	if executable == "" || !executableInScope(scope, executable) {
		return fmt.Errorf("%w: process %d is not inside %s", ErrProcessOutOfScope, pid, scope)
	}

	return windows.TerminateProcess(handle, 1)
}

// processRunning reports whether the process exists and has not yet exited.
func processRunning(pid int) bool {
	//nolint:gosec // G115: pid is validated as positive by callers.
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)

	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false
	}

	return exitCode == stillActive
}

// enumerateProcesses lists running processes with their full executable paths using a
// process snapshot. Processes that cannot be opened are skipped: without an image path
// they can never satisfy a containment check.
//
// The context is accepted for interface symmetry with the other platforms. The snapshot
// is a bounded local syscall, so there is nothing here that can hang.
func enumerateProcesses(_ context.Context) ([]ProcessInfo, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, err
	}

	var results []ProcessInfo

	for {
		if entry.ProcessID > 0 {
			if executable := processImagePath(entry.ProcessID); executable != "" {
				results = append(results, ProcessInfo{
					PID:        int(entry.ProcessID),
					Name:       syscall.UTF16ToString(entry.ExeFile[:]),
					Executable: executable,
				})
			}
		}

		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}

	return results, nil
}

// processImagePath returns the full path of a process's executable image, or an empty
// string when the process cannot be opened or has already exited.
func processImagePath(pid uint32) string {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)

	return imagePathForHandle(handle)
}

// imagePathForHandle returns the full path of the executable image behind an already open
// process handle, or an empty string when it cannot be read.
//
// Taking a handle rather than a pid is what lets forceKill decide scope and terminate
// against one pinned process object instead of two separate lookups by number.
func imagePathForHandle(handle windows.Handle) string {
	size := uint32(windows.MAX_PATH)
	buf := make([]uint16, size)

	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		// Paths longer than MAX_PATH need the extended limit.
		size = 32768
		buf = make([]uint16, size)
		if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
			return ""
		}
	}

	return syscall.UTF16ToString(buf[:size])
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package agentdetect

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// getParentProcessInfoWithPPID retrieves information about a process and its parent PID on Windows.
func getParentProcessInfoWithPPID(pid int) (parentProcessInfo, int, error) {
	info := parentProcessInfo{}
	parentPid := 0

	// Open the process with query rights
	//nolint:gosec // G115: pid is validated before use
	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(pid),
	)
	if err != nil {
		return info, 0, fmt.Errorf("failed to open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(handle)

	// Get the executable path
	exePath, err := getProcessImageName(handle)
	if err == nil {
		info.Executable = exePath
		info.Name = getBaseName(exePath)
	}

	// Get parent PID using NtQueryInformationProcess
	parentPid, err = getParentPid(pid)
	if err != nil {
		// Non-fatal - we still have the process info
		parentPid = 0
	}

	return info, parentPid, nil
}

// getParentPid retrieves the parent process ID using process snapshot.
func getParentPid(pid int) (int, error) {
	// Use CreateToolhelp32Snapshot to enumerate processes
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, fmt.Errorf("CreateToolhelp32Snapshot failed: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	err = windows.Process32First(snapshot, &entry)
	if err != nil {
		return 0, fmt.Errorf("Process32First failed: %w", err)
	}

	for {
		if int(entry.ProcessID) == pid {
			return int(entry.ParentProcessID), nil
		}
		err = windows.Process32Next(snapshot, &entry)
		if err != nil {
			break
		}
	}

	return 0, fmt.Errorf("process %d not found", pid)
}

// getProcessImageName retrieves the full path of the executable for a process.
func getProcessImageName(handle windows.Handle) (string, error) {
	// Start with a reasonable buffer size
	bufSize := uint32(windows.MAX_PATH)
	buf := make([]uint16, bufSize)

	err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &bufSize)
	if err != nil {
		// Try with a larger buffer
		bufSize = 32768
		buf = make([]uint16, bufSize)
		err = windows.QueryFullProcessImageName(handle, 0, &buf[0], &bufSize)
		if err != nil {
			return "", fmt.Errorf("QueryFullProcessImageName failed: %w", err)
		}
	}

	return syscall.UTF16ToString(buf[:bufSize]), nil
}

// getBaseName extracts the file name from a full path.
func getBaseName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '\\' || path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

// Ensure we have the right imports for unsafe operations
var _ = unsafe.Sizeof(0)

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package azdext

import (
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// isProcessRunningOS checks if a process is running on Windows using the
// Windows API. Opens the process with PROCESS_QUERY_LIMITED_INFORMATION and
// checks whether the exit code is STILL_ACTIVE (259).
func isProcessRunningOS(pid int) bool {
	//nolint:gosec // G115: pid is validated in caller
	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(pid),
	)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)

	var exitCode uint32
	err = windows.GetExitCodeProcess(handle, &exitCode)
	if err != nil {
		return false
	}

	// STILL_ACTIVE (259) means the process has not exited.
	return exitCode == 259
}

// getProcessInfoOS retrieves process info on Windows using the Windows API.
func getProcessInfoOS(pid int) ProcessInfo {
	info := ProcessInfo{PID: pid}

	//nolint:gosec // G115: pid is validated in caller
	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(pid),
	)
	if err != nil {
		return info
	}
	defer windows.CloseHandle(handle)

	// Check if process is still running.
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return info
	}
	if exitCode != 259 {
		return info // Process has exited.
	}

	info.Running = true

	// Get executable path.
	bufSize := uint32(windows.MAX_PATH)
	buf := make([]uint16, bufSize)
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &bufSize); err != nil {
		// Try with a larger buffer.
		bufSize = 32768
		buf = make([]uint16, bufSize)
		if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &bufSize); err != nil {
			return info
		}
	}

	info.Executable = syscall.UTF16ToString(buf[:bufSize])
	info.Name = extractBaseName(info.Executable)

	return info
}

// findProcessByNameOS searches for processes by name on Windows using
// CreateToolhelp32Snapshot.
func findProcessByNameOS(name string) []ProcessInfo {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return []ProcessInfo{}
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	if err := windows.Process32First(snapshot, &entry); err != nil {
		return []ProcessInfo{}
	}

	nameLower := strings.ToLower(name)
	var results []ProcessInfo

	for {
		exeName := syscall.UTF16ToString(entry.ExeFile[:])
		baseName := extractBaseName(exeName)

		if strings.EqualFold(baseName, nameLower) {
			info := ProcessInfo{
				PID:     int(entry.ProcessID),
				Name:    baseName,
				Running: true,
			}

			// Try to get full executable path.
			//nolint:gosec // G115: PID comes from OS snapshot
			handle, err := windows.OpenProcess(
				windows.PROCESS_QUERY_LIMITED_INFORMATION,
				false,
				entry.ProcessID,
			)
			if err == nil {
				bufSize := uint32(windows.MAX_PATH)
				buf := make([]uint16, bufSize)
				if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &bufSize); err == nil {
					info.Executable = syscall.UTF16ToString(buf[:bufSize])
				}
				windows.CloseHandle(handle)
			}

			results = append(results, info)
		}

		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}

	if results == nil {
		return []ProcessInfo{}
	}
	return results
}

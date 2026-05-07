// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows && !darwin

package azdext

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// isProcessRunningOS checks if a process is running on Linux using signal 0.
func isProcessRunningOS(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 does not send a signal but performs error checking.
	// If the process exists, err is nil. If it doesn't, err is non-nil.
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// getProcessInfoOS retrieves process info on Linux via /proc.
func getProcessInfoOS(pid int) ProcessInfo {
	info := ProcessInfo{PID: pid}

	// Read process name from /proc/<pid>/comm.
	commPath := fmt.Sprintf("/proc/%d/comm", pid)
	commData, err := os.ReadFile(commPath)
	if err != nil {
		return info // Process does not exist or is inaccessible.
	}
	info.Name = strings.TrimSpace(string(commData))
	info.Running = true

	// Read executable symlink from /proc/<pid>/exe.
	exePath := fmt.Sprintf("/proc/%d/exe", pid)
	exe, err := os.Readlink(exePath)
	if err == nil {
		info.Executable = exe
	}

	return info
}

// findProcessByNameOS searches for processes by name on Linux via /proc.
func findProcessByNameOS(name string) []ProcessInfo {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return []ProcessInfo{}
	}

	nameLower := strings.ToLower(name)
	var results []ProcessInfo

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue // Not a PID directory.
		}

		commPath := fmt.Sprintf("/proc/%d/comm", pid)
		commData, err := os.ReadFile(commPath)
		if err != nil {
			continue
		}

		procName := strings.TrimSpace(string(commData))
		if strings.EqualFold(procName, nameLower) {
			info := ProcessInfo{
				PID:     pid,
				Name:    procName,
				Running: true,
			}
			// Try to get executable path.
			exePath := fmt.Sprintf("/proc/%d/exe", pid)
			if exe, err := os.Readlink(exePath); err == nil {
				info.Executable = exe
			}
			results = append(results, info)
		}
	}

	if results == nil {
		return []ProcessInfo{}
	}
	return results
}

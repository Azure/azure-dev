// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build darwin

package azdext

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// isProcessRunningOS checks if a process is running on macOS using signal 0.
func isProcessRunningOS(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// getProcessInfoOS retrieves process info on macOS using ps(1).
func getProcessInfoOS(pid int) ProcessInfo {
	info := ProcessInfo{PID: pid}

	// Use ps to get process name and executable path.
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=")
	output, err := cmd.Output()
	if err != nil {
		return info // Process does not exist or is inaccessible.
	}

	info.Name = extractBaseName(strings.TrimSpace(string(output)))
	info.Running = true

	// Get full command path.
	cmd = exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=")
	output, err = cmd.Output()
	if err == nil {
		args := strings.TrimSpace(string(output))
		if fields := strings.Fields(args); len(fields) > 0 {
			info.Executable = fields[0]
		}
	}

	return info
}

// findProcessByNameOS searches for processes by name on macOS using ps(1).
func findProcessByNameOS(name string) []ProcessInfo {
	// ps -ax -o pid=,comm= lists all processes with PID and command name.
	cmd := exec.Command("ps", "-ax", "-o", "pid=,comm=")
	output, err := cmd.Output()
	if err != nil {
		return []ProcessInfo{}
	}

	nameLower := strings.ToLower(name)
	var results []ProcessInfo

	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Format: "  PID COMM"
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}

		// The comm field is the rest of the line after PID.
		procPath := strings.Join(fields[1:], " ")
		procName := extractBaseName(procPath)

		if strings.EqualFold(procName, nameLower) {
			results = append(results, ProcessInfo{
				PID:        pid,
				Name:       procName,
				Executable: procPath,
				Running:    true,
			})
		}
	}

	if results == nil {
		return []ProcessInfo{}
	}
	return results
}

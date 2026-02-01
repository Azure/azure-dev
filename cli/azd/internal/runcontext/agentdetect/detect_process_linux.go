// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build linux

package agentdetect

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// getParentProcessInfoWithPPID retrieves information about a process and its parent PID on Linux.
// It reads from /proc filesystem which is always available on Linux.
func getParentProcessInfoWithPPID(pid int) (parentProcessInfo, int, error) {
	info := parentProcessInfo{}
	parentPid := 0

	// Read process name from /proc/{pid}/comm
	commPath := fmt.Sprintf("/proc/%d/comm", pid)
	commData, err := os.ReadFile(commPath)
	if err != nil {
		return info, 0, fmt.Errorf("failed to read process comm: %w", err)
	}
	info.Name = strings.TrimSpace(string(commData))

	// Read executable path from /proc/{pid}/exe symlink
	exePath := fmt.Sprintf("/proc/%d/exe", pid)
	exe, err := os.Readlink(exePath)
	if err == nil {
		info.Executable = exe
	}
	// exe may fail due to permissions, that's ok

	// Read command line from /proc/{pid}/cmdline
	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)
	cmdlineData, err := os.ReadFile(cmdlinePath)
	if err == nil {
		// cmdline is null-separated
		info.CommandLine = strings.ReplaceAll(string(cmdlineData), "\x00", " ")
		info.CommandLine = strings.TrimSpace(info.CommandLine)
	}

	// Read parent PID from /proc/{pid}/stat
	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	statData, err := os.ReadFile(statPath)
	if err == nil {
		parentPid = parseParentPidFromStat(string(statData))
	}

	return info, parentPid, nil
}

// parseParentPidFromStat extracts the parent PID from /proc/{pid}/stat content.
// Format: pid (comm) state ppid ...
// The comm field can contain spaces and parentheses, so we find the last ')' first.
func parseParentPidFromStat(stat string) int {
	// Find the last ')' which ends the comm field
	lastParen := strings.LastIndex(stat, ")")
	if lastParen == -1 || lastParen+2 >= len(stat) {
		return 0
	}

	// After ') ' comes: state ppid ...
	rest := stat[lastParen+2:]
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return 0
	}

	// fields[0] is state, fields[1] is ppid
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0
	}
	return ppid
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows && !darwin

package processutil

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// deletedSuffix is what Linux appends to /proc/<pid>/exe once the executable has been
// unlinked or moved. That is exactly the state azd creates when it relocates a locked
// binary, so the suffix has to be stripped or the processes this package exists to find
// become invisible to it.
const deletedSuffix = " (deleted)"

// enumerateProcesses lists running processes by reading /proc.
//
// The context is accepted for interface symmetry with the other platforms. Reading /proc
// is local and bounded, so there is nothing here that can hang the way an external
// command can.
func enumerateProcesses(_ context.Context) ([]ProcessInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	var results []ProcessInfo

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue // Not a process directory.
		}

		// Processes exit constantly while /proc is walked and some belong to other
		// users, so an unreadable link is skipped rather than failing the scan.
		target, err := os.Readlink(filepath.Join("/proc", entry.Name(), "exe"))
		if err != nil {
			continue
		}

		executable := normalizeProcExe(target)
		if executable == "" {
			continue
		}

		results = append(results, ProcessInfo{
			PID:        pid,
			Name:       filepath.Base(executable),
			Executable: executable,
		})
	}

	return results, nil
}

// normalizeProcExe strips the marker Linux appends once an executable has been unlinked,
// returning the original path.
func normalizeProcExe(target string) string {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(target), deletedSuffix))
}

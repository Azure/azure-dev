// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build linux

package exec

import (
	"os"
	"path/filepath"
	"strconv"
)

func processIDsWithEnvironment(marker string) []int {
	if marker == "" {
		return nil
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}

	processIDs := []int{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}

		path := filepath.Join("/proc", entry.Name(), "environ")
		environ, err := os.ReadFile(path) //nolint:gosec // entry is a numeric process ID from /proc
		if err == nil && containsEnvironmentEntry(environ, marker) {
			processIDs = append(processIDs, pid)
		}
	}
	return processIDs
}

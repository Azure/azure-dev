// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build linux

package process

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
)

func processIDsWithTreeID(ctx context.Context, processTreeID string) []int {
	if processTreeID == "" || ctx.Err() != nil {
		return nil
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}

	processIDs := []int{}
	for _, entry := range entries {
		if ctx.Err() != nil {
			break
		}

		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}

		path := filepath.Join("/proc", entry.Name(), "environ")
		environ, err := os.ReadFile(path) //nolint:gosec // entry is a numeric process ID from /proc
		if err == nil && containsProcessTreeID(environ, processTreeID) {
			processIDs = append(processIDs, pid)
		}
	}
	return processIDs
}

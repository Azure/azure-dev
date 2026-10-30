// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build darwin

package process

import (
	"context"

	"golang.org/x/sys/unix"
)

func processIDsWithTreeID(ctx context.Context, processTreeID string) []int {
	if processTreeID == "" || ctx.Err() != nil {
		return nil
	}

	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil
	}

	processIDs := []int{}
	currentUserID := uint32(unix.Getuid())
	for _, process := range processes {
		if ctx.Err() != nil {
			break
		}

		pid := int(process.Proc.P_pid)
		if pid <= 0 || process.Eproc.Ucred.Uid != currentUserID {
			continue
		}

		environ, err := unix.SysctlRaw("kern.procargs2", pid)
		if err == nil && containsProcessTreeID(environ, processTreeID) {
			processIDs = append(processIDs, pid)
		}
	}
	return processIDs
}

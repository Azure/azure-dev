// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build darwin

package exec

import "golang.org/x/sys/unix"

func processIDsWithEnvironment(marker string) []int {
	if marker == "" {
		return nil
	}

	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil
	}

	processIDs := []int{}
	currentUserID := uint32(unix.Getuid())
	for _, process := range processes {
		pid := int(process.Proc.P_pid)
		if pid <= 0 || process.Eproc.Ucred.Uid != currentUserID {
			continue
		}

		environ, err := unix.SysctlRaw("kern.procargs2", pid)
		if err == nil && containsEnvironmentEntry(environ, marker) {
			processIDs = append(processIDs, pid)
		}
	}
	return processIDs
}

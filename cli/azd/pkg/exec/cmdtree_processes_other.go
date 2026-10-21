// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows && !linux && !darwin

package exec

import (
	"os/exec"
	"strconv"
	"strings"
)

func processIDsWithEnvironment(marker string) []int {
	if marker == "" {
		return nil
	}

	output, err := exec.Command("ps", "eww", "-A", "-o", "pid=", "-o", "command=").Output()
	if err != nil {
		return nil
	}

	processIDs := []int{}
	for line := range strings.SplitSeq(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.Contains(line, marker) {
			continue
		}

		pid, err := strconv.Atoi(fields[0])
		if err == nil && pid > 0 {
			processIDs = append(processIDs, pid)
		}
	}
	return processIDs
}

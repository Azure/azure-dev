// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows && !linux && !darwin

package process

import (
	"context"
	"strconv"
	"strings"
)

func processIDsWithTreeID(ctx context.Context, processTreeID string) []int {
	if processTreeID == "" || ctx.Err() != nil {
		return nil
	}

	output, err := processDiscoveryOutput(
		ctx,
		"eww",
		"-A",
		"-o",
		"pid=",
		"-o",
		"command=",
	)
	if err != nil {
		return nil
	}

	processIDs := []int{}
	for line := range strings.SplitSeq(string(output), "\n") {
		if ctx.Err() != nil {
			break
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		owned := false
		for _, field := range fields[1:] {
			if processTreeEnvironmentEntryContainsID([]byte(field), processTreeID) {
				owned = true
				break
			}
		}
		if !owned {
			continue
		}

		pid, err := strconv.Atoi(fields[0])
		if err == nil && pid > 0 {
			processIDs = append(processIDs, pid)
		}
	}
	return processIDs
}

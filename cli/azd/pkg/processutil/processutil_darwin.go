// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build darwin

package processutil

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// psPath is the absolute path to ps(1). Resolving through $PATH would let a caller-
// controlled environment decide which binary azd asks for the process list, and that
// list is what --force uses to choose what to terminate.
const psPath = "/bin/ps"

// enumerateProcesses lists running processes using ps(1). The comm column reports the
// full executable path on macOS, which is what scoping by directory requires. argv[0] is
// deliberately not used: it is caller-controlled and need not be a real path.
//
// -ww disables width based truncation. Without it ps assumes a default terminal width
// when stdout is a pipe and cuts the last column, which is comm here. A truncated path
// silently fails containment, which would turn --force into a no-op that reports success.
func enumerateProcesses(ctx context.Context) ([]ProcessInfo, error) {
	output, err := exec.CommandContext(ctx, psPath, "-axww", "-o", "pid=,comm=").Output()
	if err != nil {
		return nil, err
	}

	var results []ProcessInfo

	for line := range strings.SplitSeq(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// ps pads the pid column, so the line is "<pid> <path>" after trimming. Cutting
		// on the first space keeps executable paths that contain spaces intact.
		pidField, executable, found := strings.Cut(line, " ")
		if !found {
			continue
		}

		pid, err := strconv.Atoi(pidField)
		if err != nil || pid <= 0 {
			continue
		}

		executable = strings.TrimSpace(executable)
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

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build darwin

package agentdetect

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// getParentProcessInfoWithPPID retrieves information about a process and its parent PID on macOS.
// It uses the ps command which is universally available on macOS.
func getParentProcessInfoWithPPID(pid int) (parentProcessInfo, int, error) {
	info := parentProcessInfo{}
	parentPid := 0

	// Use ps to get process info and parent PID
	// -o comm= gives just the command name, -o ppid= gives parent PID
	cmd := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "comm=,ppid=")
	output, err := cmd.Output()
	if err != nil {
		return info, 0, fmt.Errorf("failed to get process info: %w", err)
	}

	// Parse output: "process_name   ppid"
	parts := strings.Fields(strings.TrimSpace(string(output)))
	if len(parts) >= 1 {
		info.Name = parts[0]
	}
	if len(parts) >= 2 {
		parentPid, _ = strconv.Atoi(parts[1])
	}

	// Get the full command path
	cmd = exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "args=")
	output, err = cmd.Output()
	if err == nil {
		cmdLine := strings.TrimSpace(string(output))
		info.CommandLine = cmdLine

		// Extract executable path (first argument)
		cmdParts := strings.Fields(cmdLine)
		if len(cmdParts) > 0 {
			info.Executable = cmdParts[0]
		}
	}

	// If we couldn't get the executable from args, try lsof
	if info.Executable == "" {
		cmd = exec.Command("lsof", "-p", fmt.Sprintf("%d", pid), "-Fn")
		output, err = cmd.Output()
		if err == nil {
			// Parse lsof output - lines starting with 'n' contain file names
			lines := bytes.Split(output, []byte("\n"))
			for _, line := range lines {
				if len(line) > 1 && line[0] == 'n' {
					path := string(line[1:])
					// Skip non-file entries
					if strings.HasPrefix(path, "/") && !strings.Contains(path, " ") {
						info.Executable = path
						break
					}
				}
			}
		}
	}

	return info, parentPid, nil
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package scripting

import (
	"os/exec"
	"syscall"
)

// setupProcessTree configures the command to run in its own process group
// so the entire tree can be killed on cancellation. Interactive mode skips
// this because Setpgid breaks stdin forwarding (same as pkg/exec/CmdTree).
func setupProcessTree(cmd *exec.Cmd, interactive bool) {
	if !interactive {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
}

// startProcessTree starts the command and returns a kill function that
// sends SIGKILL to the entire process group.
func startProcessTree(cmd *exec.Cmd) (kill func(), _ error) {
	if err := cmd.Start(); err != nil {
		return func() {}, err
	}
	return func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}, nil
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package exec

import (
	"os/exec"
	"syscall"
)

// CmdTree represents an exec.Cmd. Non-interactive commands run inside their
// own process group, while interactive commands inherit the parent's group so
// they can use the same terminal.
type CmdTree struct {
	CmdTreeOptions
	*exec.Cmd
}

func (o *CmdTree) Start() error {
	// Interactive commands like `gh auth login` should be created as
	// a fork child process in posix OS so it can use the same stdin
	// Non interactive commands like `gh auth status` can be spawn
	// wit a new process group (not as a child process) as it won't
	// require stdin to interact with the user
	if !o.Interactive {
		o.Cmd.SysProcAttr = &syscall.SysProcAttr{
			Setpgid: true,
		}
	}

	return o.Cmd.Start()
}

func (o *CmdTree) Kill() {
	if o.Cmd == nil || o.Cmd.Process == nil {
		return
	}
	if o.Interactive {
		_ = o.Cmd.Process.Kill()
		return
	}

	_ = syscall.Kill(-o.Cmd.Process.Pid, syscall.SIGKILL)
}

func isInterruptExit(err exec.ExitError, exitCode int) bool {
	if exitCode == 130 {
		return true
	}
	if err.ProcessState == nil {
		return false
	}

	waitStatus, ok := err.Sys().(syscall.WaitStatus)
	return ok && waitStatus.Signaled() && waitStatus.Signal() == syscall.SIGINT
}

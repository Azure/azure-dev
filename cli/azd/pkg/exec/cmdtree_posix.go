// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows
// +build !windows

package exec

import (
	"os/exec"
	"syscall"
)

// CmdTree represents an `exec.Cmd` run inside a process group. When
// `Kill` is called, SIGKILL is sent to the process group, which will
// kill any lingering child processes launched by the root process.
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
	_ = syscall.Kill(-o.Cmd.Process.Pid, syscall.SIGKILL)
}

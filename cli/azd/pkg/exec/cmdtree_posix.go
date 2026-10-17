// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package exec

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
	if o.Cmd != nil && o.Cmd.Cancel != nil {
		// Snapshot interactive descendants before CommandContext kills and
		// reparents the root process.
		o.Cmd.Cancel = o.kill
	}

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
	_ = o.kill()
}

func (o *CmdTree) kill() error {
	if o.Cmd == nil || o.Cmd.Process == nil {
		return os.ErrProcessDone
	}
	if o.Interactive {
		descendants := descendantProcessIDs(o.Cmd.Process.Pid)
		err := o.Cmd.Process.Kill()
		for _, pid := range descendants {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		return err
	}

	err := syscall.Kill(-o.Cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

func descendantProcessIDs(rootPID int) []int {
	output, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "ppid=").Output()
	if err != nil {
		return nil
	}

	childrenByParent := map[int][]int{}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parentPID, parentErr := strconv.Atoi(fields[1])
		if pidErr != nil || parentErr != nil || pid <= 0 {
			continue
		}
		childrenByParent[parentPID] = append(childrenByParent[parentPID], pid)
	}

	descendants := []int{}
	pending := []int{rootPID}
	seen := map[int]struct{}{rootPID: {}}
	for len(pending) > 0 {
		parentPID := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		for _, childPID := range childrenByParent[parentPID] {
			if _, ok := seen[childPID]; ok {
				continue
			}
			seen[childPID] = struct{}{}
			descendants = append(descendants, childPID)
			pending = append(pending, childPID)
		}
	}
	return descendants
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

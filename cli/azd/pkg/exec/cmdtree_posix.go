// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package exec

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
)

const processTreeEnv = "AZD_INTERNAL_PROCESS_TREE_ID"

var processTreeSequence atomic.Uint64

// CmdTree represents an exec.Cmd. Non-interactive commands run inside their
// own process group, while interactive commands inherit the parent's group so
// they can use the same terminal.
type CmdTree struct {
	CmdTreeOptions
	*exec.Cmd
	processTreeMarker string
}

func (o *CmdTree) Start() error {
	if o.Cmd != nil && o.Cmd.Cancel != nil {
		// Replace CommandContext's root-only cancellation with process-tree cleanup.
		o.Cmd.Cancel = o.kill
	}

	// Interactive commands remain in the azd process group so they can use the same
	// terminal. Non-interactive commands use a dedicated process group.
	if !o.Interactive {
		o.Cmd.SysProcAttr = &syscall.SysProcAttr{
			Setpgid: true,
		}
	}

	if o.Interactive {
		// Descendants inherit this marker, preserving ownership after the root
		// exits and parent-child relationships disappear.
		o.processTreeMarker = fmt.Sprintf(
			"%s=%d-%d",
			processTreeEnv,
			os.Getpid(),
			processTreeSequence.Add(1),
		)
		o.Cmd.Env = append(o.Cmd.Environ(), o.processTreeMarker)
	}

	if err := o.Cmd.Start(); err != nil {
		return err
	}

	return nil
}

func (o *CmdTree) Kill() {
	if err := o.kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		log.Printf("failed to terminate process tree: %v\n", err)
	}
}

func (o *CmdTree) kill() error {
	if o.Cmd == nil || o.Cmd.Process == nil {
		return os.ErrProcessDone
	}
	if o.Interactive {
		descendants := ownedProcessIDs(o.Cmd.Process.Pid, o.processTreeMarker)
		rootErr := o.Cmd.Process.Kill()
		var descendantErr error
		for _, pid := range descendants {
			if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				descendantErr = errors.Join(
					descendantErr,
					fmt.Errorf("failed to kill descendant process %d: %w", pid, err),
				)
			}
		}
		if descendantErr != nil {
			if rootErr != nil && !errors.Is(rootErr, os.ErrProcessDone) {
				return errors.Join(rootErr, descendantErr)
			}
			return descendantErr
		}
		return rootErr
	}

	err := syscall.Kill(-o.Cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

func ownedProcessIDs(rootPID int, marker string) []int {
	candidates := append(descendantProcessIDs(rootPID), processIDsWithEnvironment(marker)...)
	seen := map[int]struct{}{rootPID: {}}
	owned := make([]int, 0, len(candidates))
	for _, pid := range candidates {
		if pid <= 0 {
			continue
		}
		if _, ok := seen[pid]; ok {
			continue
		}
		seen[pid] = struct{}{}
		owned = append(owned, pid)
	}
	return owned
}

func containsEnvironmentEntry(environ []byte, marker string) bool {
	target := []byte(marker)
	for entry := range bytes.SplitSeq(environ, []byte{0}) {
		if bytes.Equal(entry, target) {
			return true
		}
	}
	return false
}

func descendantProcessIDs(rootPID int) []int {
	output, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "ppid=").Output()
	if err != nil {
		return nil
	}

	childrenByParent := map[int][]int{}
	for line := range strings.SplitSeq(string(output), "\n") {
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

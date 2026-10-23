// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package process

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const processTreeEnv = "AZD_INTERNAL_PROCESS_TREE_ID"

// Scan again because descendants can detach or spawn while termination is in progress.
const processTerminationDiscoveryPasses = 3

type platformState struct {
	processTreeMarker string
}

func configure(t *Tree) {
	t.platform.processTreeMarker = fmt.Sprintf("%s=%s", processTreeEnv, rand.Text())
	t.Cmd.Env = append(t.Cmd.Environ(), t.platform.processTreeMarker)

	if !t.options.Interactive {
		if t.Cmd.SysProcAttr == nil {
			t.Cmd.SysProcAttr = &syscall.SysProcAttr{}
		} else {
			attributes := *t.Cmd.SysProcAttr
			t.Cmd.SysProcAttr = &attributes
		}
		t.Cmd.SysProcAttr.Setpgid = true
	}
}

func finishStart(*Tree) error {
	return nil
}

func startFailed(*Tree, error) {}

// IsInterruptExit reports whether an exit status represents a Ctrl+C interrupt.
func IsInterruptExit(exitErr *exec.ExitError, exitCode int) bool {
	if IsInterruptExitCode(exitCode) {
		return true
	}
	if exitErr == nil || exitErr.ProcessState == nil {
		return false
	}

	waitStatus, ok := exitErr.Sys().(syscall.WaitStatus)
	return ok && waitStatus.Signaled() && waitStatus.Signal() == syscall.SIGINT
}

// IsInterruptExitCode reports whether exitCode is the conventional Ctrl+C exit code.
func IsInterruptExitCode(exitCode int) bool {
	return exitCode == 130
}

func terminate(t *Tree) error {
	return terminateTree(t, false)
}

func terminateRemaining(t *Tree) error {
	return terminateTree(t, true)
}

func terminateTree(t *Tree, allowFinished bool) error {
	if !allowFinished && t.finished.Load() {
		return os.ErrProcessDone
	}
	if t.Cmd.Process == nil {
		return os.ErrProcessDone
	}

	if t.options.Interactive {
		return terminateOwnedTree(t, t.Cmd.Process.Kill)
	}

	return terminateOwnedTree(t, func() error {
		err := syscall.Kill(-t.Cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	})
}

func terminateOwnedTree(
	t *Tree,
	terminateRoot func() error,
) error {
	descendants := ownedProcessIDs(
		t.Cmd.Process.Pid,
		t.platform.processTreeMarker,
	)

	rootErr := terminateRoot()
	terminatedDescendant := false
	var descendantErr error
	for attempt := range processTerminationDiscoveryPasses {
		passTerminated, passErr := terminateProcessIDs(descendants)
		terminatedDescendant = terminatedDescendant || passTerminated
		descendantErr = errors.Join(descendantErr, passErr)

		if attempt == processTerminationDiscoveryPasses-1 {
			break
		}
		descendants = ownedProcessIDs(
			t.Cmd.Process.Pid,
			t.platform.processTreeMarker,
		)
		if len(descendants) == 0 {
			break
		}
	}

	return processTerminationError(rootErr, descendantErr, terminatedDescendant)
}

func terminateProcessIDs(processIDs []int) (bool, error) {
	terminated := false
	var errs error
	for _, pid := range processIDs {
		err := syscall.Kill(pid, syscall.SIGKILL)
		switch {
		case err == nil:
			terminated = true
		case errors.Is(err, syscall.ESRCH):
		default:
			errs = errors.Join(
				errs,
				fmt.Errorf("killing descendant process %d: %w", pid, err),
			)
		}
	}
	return terminated, errs
}

func processTerminationError(rootErr, descendantErr error, terminatedDescendant bool) error {
	if descendantErr != nil {
		if rootErr != nil && !errors.Is(rootErr, os.ErrProcessDone) {
			return errors.Join(rootErr, descendantErr)
		}
		return descendantErr
	}
	if rootErr == nil ||
		(terminatedDescendant && errors.Is(rootErr, os.ErrProcessDone)) {
		return nil
	}
	return rootErr
}

func ownedProcessIDs(rootPID int, marker string) []int {
	candidates := append(
		descendantProcessIDs(rootPID),
		processIDsWithEnvironment(marker)...,
	)
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

func release(*Tree, bool) error {
	return nil
}

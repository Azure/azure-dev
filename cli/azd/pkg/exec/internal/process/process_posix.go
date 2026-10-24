// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package process

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	processTreeEnv            = "AZD_INTERNAL_PROCESS_TREE_ID"
	processTreeIDSeparator    = ","
	processDiscoveryTimeout   = time.Second
	processDiscoveryWaitDelay = 100 * time.Millisecond
)

// Scan again because descendants can detach or spawn while termination is in progress.
const processTerminationDiscoveryPasses = 3

type platformState struct {
	processTreeID string
}

func configure(t *Tree) {
	t.platform.processTreeID = rand.Text()
	t.Cmd.Env = appendProcessTreeID(t.Cmd.Environ(), t.platform.processTreeID)

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
		return terminateProcessGroupAndRoot(t.Cmd.Process)
	})
}

func terminateProcessGroupAndRoot(process *os.Process) error {
	groupErr := syscall.Kill(-process.Pid, syscall.SIGKILL)
	if errors.Is(groupErr, syscall.ESRCH) {
		groupErr = os.ErrProcessDone
	}
	rootErr := process.Kill()

	switch {
	case rootErr == nil:
		if groupErr == nil || errors.Is(groupErr, os.ErrProcessDone) {
			return nil
		}
		return groupErr
	case errors.Is(rootErr, os.ErrProcessDone):
		return groupErr
	case groupErr == nil || errors.Is(groupErr, os.ErrProcessDone):
		return rootErr
	default:
		return errors.Join(groupErr, rootErr)
	}
}

func terminateOwnedTree(
	t *Tree,
	terminateRoot func() error,
) error {
	// Cleanup runs after the execution context is canceled, so discovery needs
	// an independent deadline to remain usable without delaying termination.
	discoveryCtx, cancelDiscovery := context.WithTimeout(
		context.Background(),
		processDiscoveryTimeout,
	)
	defer cancelDiscovery()

	descendants := ownedProcessIDs(
		discoveryCtx,
		t.Cmd.Process.Pid,
		t.platform.processTreeID,
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
			discoveryCtx,
			t.Cmd.Process.Pid,
			t.platform.processTreeID,
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

func appendProcessTreeID(environ []string, processTreeID string) []string {
	prefix := processTreeEnv + "="
	inheritedIDs := ""
	updated := make([]string, 0, len(environ)+1)
	for _, entry := range environ {
		if after, ok := strings.CutPrefix(entry, prefix); ok {
			inheritedIDs = after
			continue
		}
		updated = append(updated, entry)
	}

	value := processTreeID
	if inheritedIDs != "" {
		value = inheritedIDs + processTreeIDSeparator + processTreeID
	}
	return append(updated, prefix+value)
}

func ownedProcessIDs(
	ctx context.Context,
	rootPID int,
	processTreeID string,
) []int {
	candidates := append(
		processIDsWithTreeID(ctx, processTreeID),
		descendantProcessIDs(ctx, rootPID)...,
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

func containsProcessTreeID(environ []byte, processTreeID string) bool {
	for entry := range bytes.SplitSeq(environ, []byte{0}) {
		if processTreeEnvironmentEntryContainsID(entry, processTreeID) {
			return true
		}
	}
	return false
}

func processTreeEnvironmentEntryContainsID(entry []byte, processTreeID string) bool {
	value, ok := bytes.CutPrefix(entry, []byte(processTreeEnv+"="))
	if !ok {
		return false
	}
	for candidate := range bytes.SplitSeq(value, []byte(processTreeIDSeparator)) {
		if bytes.Equal(candidate, []byte(processTreeID)) {
			return true
		}
	}
	return false
}

func descendantProcessIDs(ctx context.Context, rootPID int) []int {
	output, err := processDiscoveryOutput(
		ctx,
		"-A",
		"-o",
		"pid=",
		"-o",
		"ppid=",
	)
	if err != nil {
		return nil
	}

	childrenByParent := map[int][]int{}
	for line := range strings.SplitSeq(string(output), "\n") {
		if ctx.Err() != nil {
			break
		}

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
		if ctx.Err() != nil {
			break
		}

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

func processDiscoveryOutput(ctx context.Context, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, "ps", args...) //nolint:gosec // fixed executable and internal arguments
	cmd.WaitDelay = processDiscoveryWaitDelay
	return cmd.Output()
}

func release(*Tree, bool) error {
	return nil
}

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

func terminateOnWait(t *Tree) error {
	if t.Cmd.Process == nil || t.options.Interactive {
		return os.ErrProcessDone
	}
	return terminateProcessGroup(t.Cmd.Process.Pid)
}

func terminateTree(t *Tree, allowFinished bool) error {
	if !allowFinished && t.finished.Load() {
		return os.ErrProcessDone
	}
	if t.Cmd.Process == nil {
		return os.ErrProcessDone
	}

	discoverAncestry := !allowFinished && processMayBeRunning(t.Cmd.Process)
	if t.options.Interactive {
		return terminateOwnedTree(t, t.Cmd.Process.Kill, discoverAncestry)
	}

	return terminateOwnedTree(t, func() error {
		return terminateProcessGroupAndRoot(t.Cmd.Process)
	}, discoverAncestry)
}

func processMayBeRunning(process *os.Process) bool {
	return !errors.Is(process.Signal(syscall.Signal(0)), os.ErrProcessDone)
}

func terminateProcessGroup(processID int) error {
	err := syscall.Kill(-processID, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

func terminateProcessGroupAndRoot(process *os.Process) error {
	groupErr := terminateProcessGroup(process.Pid)
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
	discoverAncestry bool,
) error {
	// Cleanup cannot rely on the execution context, which may already be canceled.
	// Use an independent deadline so discovery cannot delay termination indefinitely.
	discoveryCtx, cancelDiscovery := context.WithTimeout(
		context.Background(),
		processDiscoveryTimeout,
	)
	defer cancelDiscovery()

	markerDiscovery := startProcessIDDiscovery(func() []int {
		return processIDsWithTreeID(discoveryCtx, t.platform.processTreeID)
	})

	var descendants []int
	if discoverAncestry {
		ancestryDiscovery := startProcessIDDiscovery(func() []int {
			return descendantProcessIDs(discoveryCtx, t.Cmd.Process.Pid)
		})
		descendants = waitForProcessIDDiscovery(discoveryCtx, ancestryDiscovery)
		if !processMayBeRunning(t.Cmd.Process) {
			descendants = nil
		}
	}

	rootErr := terminateRoot()
	descendants = uniqueProcessIDs(
		t.Cmd.Process.Pid,
		descendants,
		waitForProcessIDDiscovery(discoveryCtx, markerDiscovery),
	)
	terminatedDescendant := false
	var descendantErr error
	for attempt := range processTerminationDiscoveryPasses {
		passTerminated, passErr := terminateProcessIDs(descendants)
		terminatedDescendant = terminatedDescendant || passTerminated
		descendantErr = errors.Join(descendantErr, passErr)

		if attempt == processTerminationDiscoveryPasses-1 {
			break
		}
		descendants = uniqueProcessIDs(
			t.Cmd.Process.Pid,
			waitForProcessIDDiscovery(
				discoveryCtx,
				startProcessIDDiscovery(func() []int {
					return processIDsWithTreeID(
						discoveryCtx,
						t.platform.processTreeID,
					)
				}),
			),
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

func startProcessIDDiscovery(discover func() []int) <-chan []int {
	result := make(chan []int, 1)
	go func() {
		// Some OS process metadata reads cannot be interrupted by context cancellation.
		result <- discover()
	}()
	return result
}

func waitForProcessIDDiscovery(ctx context.Context, result <-chan []int) []int {
	select {
	case processIDs := <-result:
		return processIDs
	default:
	}

	select {
	case processIDs := <-result:
		return processIDs
	case <-ctx.Done():
		select {
		case processIDs := <-result:
			return processIDs
		default:
			return nil
		}
	}
}

func uniqueProcessIDs(rootPID int, processIDGroups ...[]int) []int {
	seen := map[int]struct{}{rootPID: {}}
	processIDs := []int{}
	for _, group := range processIDGroups {
		for _, pid := range group {
			if pid <= 0 {
				continue
			}
			if _, ok := seen[pid]; ok {
				continue
			}
			seen[pid] = struct{}{}
			processIDs = append(processIDs, pid)
		}
	}
	return processIDs
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

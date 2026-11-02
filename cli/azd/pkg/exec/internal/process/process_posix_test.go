// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const stalledProcessDiscoveryPIDEnv = "AZD_STALLED_PROCESS_DISCOVERY_PID_PATH"

func TestProcessTerminationError(t *testing.T) {
	descendantErr := errors.New("descendant termination failed")

	tests := []struct {
		name                 string
		rootErr              error
		descendantErr        error
		terminatedDescendant bool
		wantErr              error
	}{
		{
			name: "root and descendants terminated",
		},
		{
			name:                 "finished root with terminated descendant",
			rootErr:              os.ErrProcessDone,
			terminatedDescendant: true,
		},
		{
			name:    "finished root without terminated descendant",
			rootErr: os.ErrProcessDone,
			wantErr: os.ErrProcessDone,
		},
		{
			name:                 "root failure with terminated descendant",
			rootErr:              syscall.EPERM,
			terminatedDescendant: true,
			wantErr:              syscall.EPERM,
		},
		{
			name:          "descendant failure",
			descendantErr: descendantErr,
			wantErr:       descendantErr,
		},
		{
			name:          "root and descendant failures",
			rootErr:       syscall.EPERM,
			descendantErr: descendantErr,
			wantErr:       syscall.EPERM,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := processTerminationError(
				test.rootErr,
				test.descendantErr,
				test.terminatedDescendant,
			)

			if test.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, test.wantErr)
			}
			if test.rootErr != nil &&
				test.descendantErr != nil &&
				!errors.Is(test.rootErr, os.ErrProcessDone) {
				require.ErrorIs(t, err, test.descendantErr)
			}
		})
	}
}

func TestTerminateOwnedTreeBoundsProcessDiscovery(t *testing.T) {
	configureStalledProcessDiscovery(t)

	tree := &Tree{
		Cmd: &exec.Cmd{
			Process: &os.Process{Pid: os.Getpid()},
		},
		platform: platformState{
			processTreeID: "missing",
		},
	}
	rootTerminated := false
	start := time.Now()

	err := terminateOwnedTree(tree, func() error {
		rootTerminated = true
		return nil
	}, true)

	require.NoError(t, err)
	require.True(t, rootTerminated)
	require.Less(t, time.Since(start), 2*time.Second)
}

func TestWaitForProcessIDDiscoveryDoesNotBlockPastDeadline(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})
	defer close(unblock)
	result := startProcessIDDiscovery(func() []int {
		close(started)
		<-unblock
		return []int{123}
	})
	<-started

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	require.Empty(t, waitForProcessIDDiscovery(ctx, result))
	require.Less(t, time.Since(start), time.Second)
}

func configureStalledProcessDiscovery(t *testing.T) {
	t.Helper()

	tempDir := t.TempDir()
	pidPath := filepath.Join(tempDir, "discovery-child.pid")
	psPath := filepath.Join(tempDir, "ps")
	script := `#!/bin/sh
sleep 3 &
echo "$!" >> "$AZD_STALLED_PROCESS_DISCOVERY_PID_PATH"
wait
`
	//nolint:gosec // G306: the stalled ps fixture must be executable.
	require.NoError(t, os.WriteFile(psPath, []byte(script), 0o700))
	t.Setenv(
		"PATH",
		tempDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	t.Setenv(stalledProcessDiscoveryPIDEnv, pidPath)
	t.Cleanup(func() {
		content, err := os.ReadFile(pidPath) //nolint:gosec // test-controlled temporary path
		if err != nil {
			return
		}
		for line := range strings.SplitSeq(string(content), "\n") {
			pid, err := strconv.Atoi(strings.TrimSpace(line))
			if err == nil && pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
}

func TestConfigurePreservesInheritedProcessTreeIDs(t *testing.T) {
	outerCommand := exec.Command(os.Args[0], "-test.run=^$") //nolint:gosec
	outer := New(t.Context(), outerCommand, Options{})
	configure(outer)

	innerCommand := exec.Command(os.Args[0], "-test.run=^$") //nolint:gosec
	innerCommand.Env = outerCommand.Env
	inner := New(t.Context(), innerCommand, Options{})
	configure(inner)

	environ := []byte(strings.Join(innerCommand.Env, "\x00"))
	require.True(t, containsProcessTreeID(environ, outer.platform.processTreeID))
	require.True(t, containsProcessTreeID(environ, inner.platform.processTreeID))
	require.False(t, containsProcessTreeID(environ, "sibling"))
}

func TestProcessTreeEnvironmentEntryRequiresExactID(t *testing.T) {
	environ := []byte(
		"FIRST=value\x00" +
			processTreeEnv + "=outer,inner\x00" +
			"LAST=value\x00",
	)

	require.True(t, containsProcessTreeID(environ, "outer"))
	require.True(t, containsProcessTreeID(environ, "inner"))
	require.False(t, containsProcessTreeID(environ, "out"))
	require.False(t, containsProcessTreeID(environ, "inne"))
	require.False(t, containsProcessTreeID(environ, "sibling"))
}

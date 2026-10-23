// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package process

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTreeBeforeStart(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=TestTreeBeforeStart") //nolint:gosec
	tree := New(t.Context(), cmd, Options{})

	require.ErrorIs(t, tree.Kill(), os.ErrProcessDone)
	require.Error(t, tree.Wait())
}

func TestTreeSupportsCommandWithoutCommandContext(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$") //nolint:gosec
	tree := New(t.Context(), cmd, Options{})

	require.NoError(t, tree.Start())
	require.NoError(t, tree.Wait())
}

func TestInteractiveTreeUsesNonReusableOwnershipMarker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses Job Objects instead of ownership markers")
	}

	first := configuredOwnershipMarker(t)
	second := configuredOwnershipMarker(t)

	require.NotEqual(t, first, second)

	value := strings.TrimPrefix(first, "AZD_INTERNAL_PROCESS_TREE_ID=")
	parts := strings.Split(value, "-")
	if len(parts) == 2 {
		_, pidErr := strconv.Atoi(parts[0])
		_, sequenceErr := strconv.Atoi(parts[1])
		require.True(
			t,
			pidErr != nil || sequenceErr != nil,
			"ownership marker must not rely only on a reusable process ID and sequence",
		)
	}
}

func TestCleanupStartedProcessPreservesErrorsAndWaitsAfterKillFailure(t *testing.T) {
	killErr := errors.New("kill failed")
	waitErr := errors.New("wait failed")
	releaseErr := errors.New("release failed")
	waitCalled := false

	err := cleanupStartedProcess(
		func() error {
			return killErr
		},
		func() error {
			waitCalled = true
			return waitErr
		},
		func() error {
			return releaseErr
		},
		time.Second,
	)

	require.True(t, waitCalled)
	require.ErrorIs(t, err, killErr)
	require.ErrorIs(t, err, waitErr)
	require.ErrorIs(t, err, releaseErr)
}

func TestCleanupStartedProcessBoundsWait(t *testing.T) {
	waitStarted := make(chan struct{})
	unblockWait := make(chan struct{})
	defer close(unblockWait)

	start := time.Now()
	err := cleanupStartedProcess(
		func() error {
			return nil
		},
		func() error {
			close(waitStarted)
			<-unblockWait
			return nil
		},
		func() error {
			return nil
		},
		20*time.Millisecond,
	)

	require.ErrorIs(t, err, errFailedStartWaitTimeout)
	require.Less(t, time.Since(start), time.Second)
	select {
	case <-waitStarted:
	default:
		t.Fatal("Wait was not attempted")
	}
}

func configuredOwnershipMarker(t *testing.T) string {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run=^$") //nolint:gosec
	tree := New(t.Context(), cmd, Options{Interactive: true})
	configure(tree)

	const prefix = "AZD_INTERNAL_PROCESS_TREE_ID="
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, prefix) {
			return entry
		}
	}
	t.Fatal("interactive process ownership marker was not configured")
	return ""
}

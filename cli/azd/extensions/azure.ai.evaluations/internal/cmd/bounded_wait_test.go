// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The budget was checked only between polls, and the data-plane client carries
// no deadline of its own, so one stalled response held `--wait` open with
// nothing left to stop it -- which is the single thing a bounded wait promises.
func TestABudgetThatRanOutIsReportedAsSpentNotAsAFailure(t *testing.T) {
	t.Parallel()

	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	err := waitStopped(context.Background(), expired, "run_1")

	require.Error(t, err)
	assert.ErrorIs(t, err, errWaitBudgetSpent,
		"the run is still going server-side, so this is the reattach path")
}

// Cancelling during the last seconds of the budget is an interruption. Calling
// it a spent budget would hand back a reattach line for a wait the reader had
// already abandoned, so the caller is asked first.
func TestTheCallerGivingUpIsNotTheBudgetRunningOut(t *testing.T) {
	t.Parallel()

	caller, cancelCaller := context.WithCancel(context.Background())
	cancelCaller()
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	err := waitStopped(caller, expired, "run_1")

	require.Error(t, err)
	assert.NotErrorIs(t, err, errWaitBudgetSpent)
	assert.Contains(t, err.Error(), "run_1", "what is still running has to be named")
}

// A poll that failed for its own reasons is not the budget, and must keep
// reporting the reason it failed.
func TestAnOrdinaryPollFailureIsNotMistakenForTheBudget(t *testing.T) {
	t.Parallel()

	live, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	assert.NoError(t, waitStopped(context.Background(), live, "run_1"),
		"nothing stopped the wait, so the caller reports the real error")
}

// --force is permission to replace the destination, not to lose it. When the
// install fails and the original cannot be put back either, the original is
// sitting under a deliberately unguessable name -- so the error has to say
// where, or it is lost to the reader while still being on disk.
//
// The second failure is injected because it cannot be provoked through the
// filesystem: whatever stops the install is gone by the time the restore runs.
func TestAFailedRestoreSaysWhereTheOriginalIsBeingHeld(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "golden")
	require.NoError(t, os.MkdirAll(dest, 0o750))

	var held string
	prev := renameFunc
	calls := 0
	renameFunc = func(from, to string) error {
		calls++
		switch calls {
		case 1: // dest moved aside, which has to really happen
			held = to
			return prev(from, to)
		case 2: // the install
			return errors.New("install failed")
		default: // the restore
			return errors.New("restore failed")
		}
	}
	t.Cleanup(func() { renameFunc = prev })

	err := replaceDir(filepath.Join(root, "staging"), dest)

	require.Error(t, err)
	assert.Equal(t, 3, calls, "the restore has to be attempted before giving up")
	assert.Contains(t, err.Error(), "install failed", "what went wrong is still reported")
	assert.Contains(t, err.Error(), "restore failed", "and so is the failure to undo it")
	assert.Contains(t, err.Error(), filepath.Base(held),
		"the original is under a name nobody can guess, so the error has to name it")
}

// A restore that works is not worth alarming anyone about: the original is back
// where it was, and only the install failure is reported.
func TestARestoreThatWorksReportsOnlyTheInstallFailure(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "golden")
	require.NoError(t, os.MkdirAll(dest, 0o750))

	err := replaceDir(filepath.Join(root, "no-such-staging"), dest)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), ".azd-replaced-",
		"nothing is stranded, so nothing is named")
	assert.DirExists(t, dest, "the original is back where it was")
}

// The ordinary failure still reads as one: nothing was moved aside, so there is
// no holding path to talk about.
func TestAFailedInstallWithNothingToRestoreReadsAsAPlainWriteFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	err := replaceDir(filepath.Join(root, "no-such-staging"), filepath.Join(root, "golden"))

	require.Error(t, err)
	assert.NotContains(t, err.Error(), ".azd-replaced-",
		"nothing was set aside, so nothing is being held")
}

// Sanity: a replace that works leaves the new content in place and the holding
// directory gone.
func TestAReplaceThatWorksLeavesNothingBehind(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dest := filepath.Join(root, "golden")
	staging := filepath.Join(root, "staging")
	require.NoError(t, os.MkdirAll(dest, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dest, "old.jsonl"), []byte("old"), 0o600))
	require.NoError(t, os.MkdirAll(staging, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(staging, "new.jsonl"), []byte("new"), 0o600))

	require.NoError(t, replaceDir(staging, dest))

	assert.FileExists(t, filepath.Join(dest, "new.jsonl"))
	assert.NoFileExists(t, filepath.Join(dest, "old.jsonl"))
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, strings.HasPrefix(e.Name(), ".azd-replaced-"),
			"the holding directory is discarded once the new one is in place")
	}
}

// errors.Is has to keep working through the wrapping, or the reattach path
// stops recognising a spent budget.
func TestASpentBudgetStaysRecognisableThroughWrapping(t *testing.T) {
	t.Parallel()

	wrapped := errors.Join(errors.New("context"), errWaitBudgetSpent)
	assert.ErrorIs(t, wrapped, errWaitBudgetSpent)
}

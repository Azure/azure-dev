// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The state lock is taken, released, and reusable, and it creates azd's
// directory when the first write beats azd to it.
func TestLockEvalStateIsTakenAndReleased(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".azure")

	unlock, err := LockEvalState(context.Background(), dir)
	require.NoError(t, err)
	require.NotNil(t, unlock)
	assert.DirExists(t, dir)
	unlock()

	unlock2, err := LockEvalState(context.Background(), dir)
	require.NoError(t, err)
	unlock2()
}

// The reason this lock exists: azd deploys services concurrently by default,
// and each eval service's deploy reads the reconciliation section, changes one
// entry and writes the whole section back. Two of them running unlocked both
// report success and the later write drops the other's ids and fingerprints,
// which is not noticed until the next `azd up` publishes a second immutable
// version of every resource it can no longer recognize.
func TestLockEvalStateRefusesWhenHeld(t *testing.T) {
	restore := configLockTimeout
	configLockTimeout = 50 * time.Millisecond
	t.Cleanup(func() { configLockTimeout = restore })

	dir := filepath.Join(t.TempDir(), ".azure")

	unlock, err := LockEvalState(context.Background(), dir)
	require.NoError(t, err)
	t.Cleanup(unlock)

	second, err := LockEvalState(context.Background(), dir)

	require.Error(t, err, "a held lock must stop the second writer, not warn it")
	require.Nil(t, second, "there is no lock to release, so there is nothing to hand back")
	assert.Contains(t, err.Error(), ".azure",
		"the reader has to know what is busy")
}

// Two eval services in one project have two different `$ref` directories, so a
// lock beside either configuration would let both through. They share azd's
// directory, which is why the lock lives there.
func TestTheStateLockIsSharedByServicesWithDifferentConfigs(t *testing.T) {
	restore := configLockTimeout
	configLockTimeout = 50 * time.Millisecond
	t.Cleanup(func() { configLockTimeout = restore })

	root := t.TempDir()
	stateDir := filepath.Join(root, ".azure")

	// Standing in for two services: one under evals/, one under quality/.
	unlock, err := LockEvalState(context.Background(), stateDir)
	require.NoError(t, err)
	t.Cleanup(unlock)

	_, err = LockEvalState(context.Background(), stateDir)
	require.Error(t, err, "the second service has to wait for the first")

	// The per-configuration lock is the one that would not have held them
	// apart, and it is still free while the state lock is taken.
	configUnlock, err := LockEvalConfig(context.Background(), filepath.Join(root, "quality"))
	require.NoError(t, err, "the two locks guard different things")
	configUnlock()
}

// A symbolic link standing where the lock file goes is not a lock file, and
// both the chmod and the lock itself would act on whatever it points at.
func TestLockEvalStateRefusesALinkedLockFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	require.NoError(t, os.WriteFile(target, []byte("not a lock"), 0o600))

	if err := os.Symlink(target, filepath.Join(dir, evalStateLockName)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := LockEvalState(context.Background(), dir)

	require.Error(t, err, "a link gets to choose which file this widens to 0666")
}

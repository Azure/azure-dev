// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The lock is advisory: a scaffold must not fail because a lock file could
// not be taken, and an earlier version reported that case only through log,
// which is pointed at io.Discard unless --debug -- exactly as silent as saying
// nothing. It now reports on stderr.
func TestLockEvalConfigIsTakenAndReleased(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evals")

	unlock, err := LockEvalConfig(context.Background(), dir)
	require.NoError(t, err)
	require.NotNil(t, unlock)
	unlock()

	// And it is reusable once released.
	unlock2, err := LockEvalConfig(context.Background(), dir)
	require.NoError(t, err)
	require.NotNil(t, unlock2)
	unlock2()
}

// The lock lives beside the configuration it guards, not in the OS temp
// directory. Temp looked tidier and was wrong twice over: the file is created
// 0600 by whoever runs first, so a second user on the same machine can never
// open it and silently never locks; and two containers bind-mounting one
// project have separate temp directories, so they never see each other's lock.
func TestLockEvalConfigLivesBesideTheConfig(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evals")

	unlock, err := LockEvalConfig(context.Background(), dir)
	require.NoError(t, err)
	defer unlock()

	_, err = os.Stat(filepath.Join(dir, evalConfigLockName))
	assert.NoError(t, err, "the lock belongs in the directory it guards")
}

// Beside the config means inside a directory the user commits, so the lock has
// to keep itself out of `git status` -- which is the one thing the temp
// directory had going for it.
func TestLockEvalConfigIgnoresItself(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evals")

	unlock, err := LockEvalConfig(context.Background(), dir)
	require.NoError(t, err)
	defer unlock()

	body, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, err)
	assert.Contains(t, string(body), evalConfigLockName)
}

// A .gitignore the user maintains is theirs. Appending to it is a surprising
// edit, and a visible lock file is the far smaller problem.
func TestLockEvalConfigLeavesAnExistingGitignoreAlone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evals")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	theirs := filepath.Join(dir, ".gitignore")
	require.NoError(t, os.WriteFile(theirs, []byte("*.local\n"), 0o600))

	unlock, err := LockEvalConfig(context.Background(), dir)
	require.NoError(t, err)
	defer unlock()

	body, err := os.ReadFile(theirs)
	require.NoError(t, err)
	assert.Equal(t, "*.local\n", string(body), "the user's file is not ours to edit")
}

// cobra hands a nil context to a command that was not run through Execute, and
// waiting on a nil context panics.
func TestLockEvalConfigToleratesANilContext(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evals")

	//nolint:staticcheck // the nil context is the case under test
	unlock, err := LockEvalConfig(nil, dir)
	require.NoError(t, err)
	require.NotNil(t, unlock)
	unlock()
}

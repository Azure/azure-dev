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

// The lock is taken, released, and reusable.
//
// It used to be advisory: a lock it could not take was reported on stderr and
// the work went ahead anyway. That reintroduced the very race the lock exists
// for -- the callers read the configuration, change one entry and write the
// whole document back, so two of them running unlocked both succeed and the
// later write drops the earlier one's entry. It now refuses.
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

// A lock already held elsewhere fails the command rather than letting a second
// read-modify-write run beside the first.
//
// The message has to name the directory: a reader looking at a failed
// `generate` needs to know which configuration is busy, and that another
// process -- not their input -- is why.
func TestLockEvalConfigRefusesWhenHeld(t *testing.T) {
	// The wait itself is not what is under test, and paying the real one on
	// every run buys nothing.
	restore := configLockTimeout
	configLockTimeout = 50 * time.Millisecond
	t.Cleanup(func() { configLockTimeout = restore })

	dir := filepath.Join(t.TempDir(), "evals")

	unlock, err := LockEvalConfig(context.Background(), dir)
	require.NoError(t, err)
	t.Cleanup(unlock)

	second, err := LockEvalConfig(context.Background(), dir)

	require.Error(t, err, "a held lock must stop the second writer, not warn it")
	require.Nil(t, second, "there is no lock to release, so there is nothing to hand back")
	assert.Contains(t, err.Error(), "evals",
		"the reader has to know which configuration is busy")
}

// The lock takes the configuration file as readily as the directory.
//
// A second `init` in a scaffolded project reads back the path the first one
// recorded, and that is the configuration file. Passing it through gave
// "creating evals/azure.eval.yaml: mkdir evals\azure.eval.yaml" and failed the
// command before it had done anything -- on the one flow the instructions
// explicitly ask people to try.
func TestLockEvalConfigAcceptsTheConfigFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evals")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	configPath := filepath.Join(dir, EvalConfigBase)
	require.NoError(t, os.WriteFile(configPath, []byte("evals: []\n"), 0o600))

	unlock, err := LockEvalConfig(context.Background(), configPath)

	require.NoError(t, err, "the path a previous init recorded has to be usable")
	require.NotNil(t, unlock)
	defer unlock()

	// Beside the configuration, not inside a directory named after it.
	assert.FileExists(t, filepath.Join(dir, evalConfigLockName))
	assert.NoDirExists(t, filepath.Join(configPath, evalConfigLockName))
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

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	"azureaieval/internal/messages"

	"github.com/gofrs/flock"
)

// configLockTimeout bounds the wait for another process's read-modify-write.
// Nothing that holds this lock waits on a person -- the evaluator prompt is
// deliberately outside it -- so a wait longer than this is a stale lock rather
// than contention.
const configLockTimeout = 30 * time.Second

// evalConfigLockName is the lock file, beside the configuration it guards.
//
// Not in the OS temp directory, which looked tidier and was wrong twice over: a
// lock file there is created 0600 by whoever runs first, so a second user on
// the same machine can never open it and silently never locks; and two
// containers bind-mounting one project have separate temp directories, so they
// never see each other's lock at all. Beside the config it shares the project's
// lifetime, permissions and mount, and the `git status` noise that argued for
// temp is answered by ignoreLockFile.
const evalConfigLockName = ".azure.eval.lock"

// LockEvalConfig serialises read-modify-write on the configuration across
// processes, returning the release function and whether the lock was taken.
//
// Updating the configuration means reading the file, adding an entry and
// writing it back. Two processes doing that at once can both read the same
// state, and the second write then drops the first one's entry -- a lost update
// that reports success on both sides. The atomic write stops a reader seeing a
// half-written file; it cannot stop this.
//
// Advisory and best-effort: a lock that could not be taken is logged and the
// work goes ahead, because failing a scaffold over a lock file would be worse
// than the lost update it guards against. The boolean is what lets a caller
// tell the difference.
func LockEvalConfig(ctx context.Context, evalDir string) (func(), bool, error) {
	if ctx == nil {
		// cobra hands a nil context to a command that was not run through
		// Execute, and waiting on nil panics.
		ctx = context.Background()
	}
	if err := os.MkdirAll(evalDir, 0o750); err != nil {
		return nil, false, messages.Creating(evalDir, err)
	}

	lock := flock.New(filepath.Join(evalDir, evalConfigLockName))
	ignoreLockFile(evalDir)
	waitCtx, cancel := context.WithTimeout(ctx, configLockTimeout)
	defer cancel()

	locked, err := lock.TryLockContext(waitCtx, 50*time.Millisecond)
	if err != nil || !locked {
		// Said out loud rather than swallowed. A lost update that happened
		// because the lock was never held is otherwise unexplainable after the
		// fact, and the previous version returned a nil error on every path,
		// which made the callers' error handling dead code.
		log.Printf("[lock] proceeding without the config lock for %s (locked=%t): %v",
			evalDir, locked, err)
		return func() {}, false, nil
	}
	return func() { _ = lock.Unlock() }, true, nil
}

// ignoreLockFile keeps the lock out of `git status`, which is the one thing the
// OS temp directory had going for it.
//
// Only when there is no .gitignore of its own to respect: editing a file the
// user maintains is not this function's business, and a visible lock file is a
// far smaller problem than a surprising edit.
func ignoreLockFile(evalDir string) {
	path := filepath.Join(evalDir, ".gitignore")
	if _, err := os.Stat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
		return
	}
	_ = os.WriteFile(path, []byte(evalConfigLockName+"\n"), 0o600)
}

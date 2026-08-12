// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
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
// processes, returning the release function.
//
// Updating the configuration means reading the file, adding an entry and
// writing it back. Two processes doing that at once can both read the same
// state, and the second write then drops the first one's entry -- a lost update
// that reports success on both sides. The atomic write stops a reader seeing a
// half-written file; it cannot stop this.
//
// Advisory and best-effort: a lock that could not be taken is reported and the
// work goes ahead, because failing a scaffold over a lock file would be worse
// than the lost update it guards against. Reported on stderr rather than
// through log, which is pointed at io.Discard unless --debug -- an earlier
// version logged it and was therefore exactly as silent as saying nothing.
func LockEvalConfig(ctx context.Context, evalDir string) (func(), error) {
	if ctx == nil {
		// cobra hands a nil context to a command that was not run through
		// Execute, and waiting on nil panics.
		ctx = context.Background()
	}
	if err := os.MkdirAll(evalDir, 0o750); err != nil {
		return nil, messages.Creating(evalDir, err)
	}

	lock := flock.New(filepath.Join(evalDir, evalConfigLockName))
	waitCtx, cancel := context.WithTimeout(ctx, configLockTimeout)
	defer cancel()

	locked, err := lock.TryLockContext(waitCtx, 50*time.Millisecond)
	if err != nil || !locked {
		fmt.Fprint(os.Stderr, messages.Warning(messages.ConfigLockUnavailable(evalDir, err)))
		return func() {}, nil
	}
	// Only once the file is ours: a lock that was never taken has no artifact
	// to hide, and writing into a directory the user commits is not something
	// to do on the way past.
	ignoreLockFile(evalDir)
	return func() { _ = lock.Unlock() }, nil
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

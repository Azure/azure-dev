// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
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
//
// A variable only so a test can prove the refusal without waiting it out.
// Nothing outside this package changes it.
var configLockTimeout = 30 * time.Second

// evalConfigLockName is the lock file, beside the configuration it guards.
//
// Not in the OS temp directory: two containers bind-mounting one project have
// separate temp directories, so they never see each other's lock at all. Beside
// the config it shares the project's lifetime and mount, and the `git status`
// noise that argued for temp is answered by ignoreLockFile.
//
// Moving it does not by itself make the file openable by a second user, which
// an earlier version of this comment claimed: flock creates it 0600 whatever
// directory it sits in, so the first user to run would own a lock nobody else
// could take, and every later config mutation would fail for them. That is what
// evalConfigLockPerm is for.
const evalConfigLockName = ".azure.eval.lock"

// evalConfigLockPerm is the mode the lock file is created with.
//
// gofrs/flock defaults to 0600, which is right for a file holding something.
// This one holds nothing -- it exists to be opened -- and it sits in a
// directory two people or two containers can share, so the default locks
// everyone except its owner out of a project they can otherwise write to. The
// umask still applies.
const evalConfigLockPerm = 0o666

// LockEvalConfig serializes read-modify-write on the configuration across
// processes, returning the release function.
//
// Updating the configuration means reading the file, adding an entry and
// writing it back. Two processes doing that at once can both read the same
// state, and the second write then drops the first one's entry -- a lost update
// that reports success on both sides. The atomic write stops a reader seeing a
// half-written file; it cannot stop this.
//
// A lock that cannot be taken fails the caller. It used to be advisory -- the
// failure was printed and the work went ahead -- on the reasoning that failing
// a scaffold over a lock file was worse than the lost update. It is not: a
// scaffold that fails says so and can be run again, while a lost update is two
// commands reporting success and one author's entry quietly gone.
func LockEvalConfig(ctx context.Context, evalDir string) (func(), error) {
	if ctx == nil {
		// cobra hands a nil context to a command that was not run through
		// Execute, and waiting on nil panics.
		ctx = context.Background()
	}
	// Callers hold a location, which is the directory before anything is
	// written and the configuration file once it exists. A second `init` in a
	// scaffolded project reads back the recorded path and hands over the file,
	// and creating a directory named azure.eval.yaml then fails the command
	// before it has done anything.
	evalDir, err := ensureEvalDir(evalDir)
	if err != nil {
		return nil, err
	}

	lockPath := filepath.Join(evalDir, evalConfigLockName)
	// A lock file left at 0600 by an earlier version keeps locking the second
	// user out, and only its owner can widen it. Best effort: whoever owns it
	// repairs it the next time they run, and everyone else carries on.
	if info, statErr := os.Stat(lockPath); statErr == nil && info.Mode().Perm() != evalConfigLockPerm {
		_ = os.Chmod(lockPath, evalConfigLockPerm)
	}

	lock := flock.New(lockPath, flock.SetPermissions(evalConfigLockPerm))
	waitCtx, cancel := context.WithTimeout(ctx, configLockTimeout)
	defer cancel()

	locked, err := lock.TryLockContext(waitCtx, 50*time.Millisecond)
	if err != nil || !locked {
		// Being cancelled is not the same as the lock being busy. Carrying a
		// Ctrl-C into the report below would blame a colleague for a stop the
		// reader asked for themselves.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		// Failing is the point. The callers of this lock read the
		// configuration, change one entry and write the whole document back,
		// so two of them running unlocked both succeed and the later write
		// drops the earlier one's entry. Losing an entry silently is worse
		// than being told to run the command again.
		return nil, messages.ConfigLockUnavailable(evalDir, err)
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

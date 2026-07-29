// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"
)

// ErrLinkedDirectory indicates a path azd was about to operate on is a symlink, junction,
// or other reparse point rather than a real directory.
var ErrLinkedDirectory = errors.New("path is a link rather than a real directory")

// RequireRealDir verifies that path is a real directory rather than a symlink, junction,
// or other reparse point. A path that does not exist yet is accepted, because there is
// nothing there to redirect.
//
// azd derives both the set of files it deletes and the scope it terminates processes
// against from directory paths it builds itself. Following a link would let anything able
// to write into the extensions directory point either operation somewhere else entirely,
// so a link is refused rather than resolved.
//
// Windows reports the two forms differently and neither is caught by a single check. A
// directory symlink sets ModeSymlink and is followed by filepath.EvalSymlinks. A junction
// sets ModeIrregular, reports IsDir false, and is not resolved by EvalSymlinks at all,
// yet os.ReadDir and os.Rename still follow it. Both bits therefore have to be tested.
func RequireRealDir(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to inspect %s: %w", path, err)
	}

	if info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		return fmt.Errorf("%w: %s", ErrLinkedDirectory, path)
	}

	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrLinkedDirectory, path)
	}

	return nil
}

// RemoveAllWithRelocation removes path and everything under it, falling back to moving
// files aside when they cannot be deleted.
//
// Windows refuses to delete a file that is mapped as a running executable image, but it
// does allow that file to be renamed, because the image loader opens executables with
// FILE_SHARE_DELETE. Moving the locked file into trashDir therefore empties path and lets
// the directory itself be removed, which is what callers actually need. The relocated file
// disappears on a later SweepTrash once the holding process exits.
//
// trashDir must live outside path, otherwise relocating into it would not empty path.
func RemoveAllWithRelocation(ctx context.Context, path string, trashDir string) error {
	if path == "" {
		return errors.New("a path is required")
	}

	if trashDir == "" {
		return errors.New("a trash directory is required")
	}

	if IsPathContained(path, trashDir) {
		return fmt.Errorf("trash directory %q must not live inside %q", trashDir, path)
	}

	// Fast path. Most removals succeed outright, and going through the retrying RemoveAll
	// first would spend its whole backoff budget before relocation ever got a chance.
	if err := os.RemoveAll(path); err == nil {
		return nil
	}

	// Something under path is held open. Move the offending files out so the directory
	// can be emptied, then let the retrying removal finish the job.
	if relocated, err := relocateLockedFiles(path, trashDir); err != nil {
		log.Printf("failed to relocate locked files under %s: %v", path, err)
	} else if relocated > 0 {
		log.Printf("relocated %d locked file(s) from %s to %s", relocated, path, trashDir)
	}

	return RemoveAll(ctx, path)
}

// SweepTrash deletes files previously moved into trashDir that are no longer held open,
// and removes the directory once it is empty.
//
// It is best effort by design and reports nothing. An entry whose process is still
// running simply stays until a later sweep, and that is never a reason to fail the
// command the sweep is attached to. It takes a context for the same reason RemoveAll does:
// a large trash directory means a long run of filesystem deletes, and a cancelled command
// should stop doing work rather than finish tidying up.
func SweepTrash(ctx context.Context, trashDir string) {
	if trashDir == "" {
		return
	}

	// The sweep deletes every child of this directory, so a planted link here would turn
	// a routine cleanup into a delete of whatever it points at. Refuse rather than follow.
	if err := RequireRealDir(trashDir); err != nil {
		log.Printf("refusing to sweep %s: %v", trashDir, err)
		return
	}

	entries, err := os.ReadDir(trashDir)
	if err != nil {
		return // No trash directory, or it is unreadable. Either way there is nothing to do.
	}

	remaining := 0

	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}

		target := filepath.Join(trashDir, entry.Name())
		if err := os.RemoveAll(target); err != nil {
			remaining++
			log.Printf("trash entry %s is still in use, leaving it for a later sweep: %v", target, err)
		}
	}

	if remaining == 0 {
		// Best effort: a concurrent azd process may have written a new entry in between.
		_ = os.Remove(trashDir)
	}
}

// relocateLockedFiles moves every file under root that cannot be deleted into trashDir,
// returning how many were moved.
//
// The walk is best effort. A file that can be deleted is deleted, a file that cannot is
// renamed, and anything that resists both is reported so the caller can surface a useful
// error rather than a bare permission failure.
func relocateLockedFiles(root string, trashDir string) (int, error) {
	relocated := 0

	var failures []error

	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			// Unreadable subtrees are skipped: the final removal reports the real problem.
			return nil //nolint:nilerr // Best effort walk, the caller retries the removal.
		}

		if entry.IsDir() {
			return nil
		}

		if removeErr := os.Remove(path); removeErr == nil {
			return nil
		}

		if ensureErr := ensureTrashDir(trashDir); ensureErr != nil {
			failures = append(failures, ensureErr)
			return filepath.SkipAll
		}

		if relocateErr := relocateInto(path, trashDir, entry.Name()); relocateErr != nil {
			failures = append(failures, relocateErr)
			return nil
		}

		relocated++

		return nil
	})

	if walkErr != nil {
		failures = append(failures, walkErr)
	}

	return relocated, errors.Join(failures...)
}

// ensureTrashDir creates trashDir when it is missing and verifies it is a real directory
// rather than a link.
//
// The two halves belong together and both have to run on every use. The create is
// repeated because SweepTrash deletes the directory as soon as it observes it empty, and
// a second azd process sweeps the same shared directory. The link check is repeated, and
// deliberately runs after the create, because creating a directory does not prove the
// name was not already a link, and a link swapped in after an earlier check would
// otherwise receive the next relocated file.
func ensureTrashDir(trashDir string) error {
	if err := os.MkdirAll(trashDir, PermissionDirectory); err != nil {
		return fmt.Errorf("failed to create trash directory: %w", err)
	}

	return RequireRealDir(trashDir)
}

// relocateInto moves a single locked file into trashDir under a name no concurrent azd
// process can also have chosen.
//
// Picking a destination and renaming onto it cannot be made one operation, so a name
// derived only from what is currently on disk is inherently racy: two azd processes
// relocating the same base name both see the same free path and both rename onto it. On
// Windows os.Rename replaces the destination, so the collision is not even reported: one
// of them either clobbers a file the other still needs or fails because that destination
// is itself a locked executable, and the uninstall it belonged to fails after its
// retries. Detecting the conflict afterwards is therefore not an option, and the name
// itself has to make it impossible.
//
// The destination directory can also disappear between the caller's create and this
// rename: SweepTrash removes it the moment it observes it empty, and a second azd process
// sweeps the same shared directory. A vanished destination is therefore recreated and the
// move retried once, because giving up here would leave the locked file in place and fail
// the very removal this fallback exists to make succeed.
func relocateInto(path string, trashDir string, name string) error {
	err := os.Rename(path, uniqueTrashPath(trashDir, name))
	if err == nil {
		return nil
	}

	// Only a missing path is worth a second attempt. A permission failure, or a
	// destination that is itself a locked executable, fails again the same way.
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to relocate %s: %w", path, err)
	}

	// ensureTrashDir repeats the link check, so recreating the directory cannot be turned
	// into a redirect by a link planted in this window.
	if ensureErr := ensureTrashDir(trashDir); ensureErr != nil {
		return fmt.Errorf("failed to relocate %s: %w", path, ensureErr)
	}

	if retryErr := os.Rename(path, uniqueTrashPath(trashDir, name)); retryErr != nil {
		return fmt.Errorf("failed to relocate %s: %w", path, retryErr)
	}

	return nil
}

// uniqueTrashPath returns a destination inside trashDir that is unique to this process
// and this call, so a relocated file collides neither with one left behind by an earlier
// run nor with one another azd process is relocating at the same moment.
//
// The original base name is kept as a prefix so a leftover entry stays recognizable while
// it waits for a sweep.
func uniqueTrashPath(trashDir string, name string) string {
	var suffix [8]byte

	if _, err := rand.Read(suffix[:]); err != nil {
		// crypto/rand does not fail in practice. The process id and the clock still
		// separate this candidate from every other process's.
		return filepath.Join(trashDir, fmt.Sprintf("%s.%d.%d", name, os.Getpid(), time.Now().UnixNano()))
	}

	return filepath.Join(trashDir, fmt.Sprintf("%s.%d.%s", name, os.Getpid(), hex.EncodeToString(suffix[:])))
}

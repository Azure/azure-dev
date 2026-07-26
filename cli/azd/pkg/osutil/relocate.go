// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
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

		if mkdirErr := os.MkdirAll(trashDir, PermissionDirectory); mkdirErr != nil {
			failures = append(failures, fmt.Errorf("failed to create trash directory: %w", mkdirErr))
			return filepath.SkipAll
		}

		// Checked after the create so an existing link is caught, and on every iteration
		// so a link swapped in mid-walk cannot receive the next relocated file.
		if linkErr := RequireRealDir(trashDir); linkErr != nil {
			failures = append(failures, linkErr)
			return filepath.SkipAll
		}

		destination := uniqueTrashPath(trashDir, entry.Name())
		if renameErr := os.Rename(path, destination); renameErr != nil {
			failures = append(failures, fmt.Errorf("failed to relocate %s: %w", path, renameErr))
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

// uniqueTrashPath returns a path inside trashDir that no entry currently occupies, so a
// file relocated today does not collide with one left behind by an earlier run.
func uniqueTrashPath(trashDir string, name string) string {
	candidate := filepath.Join(trashDir, name)
	if !pathExists(candidate) {
		return candidate
	}

	for suffix := 1; suffix < 1000; suffix++ {
		candidate = filepath.Join(trashDir, fmt.Sprintf("%s.%d", name, suffix))
		if !pathExists(candidate) {
			return candidate
		}
	}

	return filepath.Join(trashDir, fmt.Sprintf("%s.%d", name, os.Getpid()))
}

// pathExists reports whether anything exists at path, without following symlinks.
func pathExists(path string) bool {
	_, err := os.Lstat(path)

	return !errors.Is(err, fs.ErrNotExist)
}

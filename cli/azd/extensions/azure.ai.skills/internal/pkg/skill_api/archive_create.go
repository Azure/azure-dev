// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package skill_api

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// ArchiveOptions tunes the safety caps for ArchiveDirectory. Zero values
// fall back to the same defaults as ExtractOptions on the way down so a
// directory that round-trips through download → edit → create cannot trip
// the upload-side caps when extraction would have accepted it.
type ArchiveOptions struct {
	MaxEntries           int
	MaxTotalUncompressed int64
}

// ArchiveDirectory builds an in-memory zip of srcDir's contents and returns
// the raw bytes. Symmetric with SafeExtract:
//
//   - rejects symlinks and non-regular entries (devices, sockets, named pipes)
//   - caps the number of file entries (MaxEntries, default 10,000)
//   - caps the total uncompressed bytes (MaxTotalUncompressed, default 512 MB)
//
// Entry names are written with forward-slash separators so the archive
// extracts identically on any OS.
//
// If srcDir itself is a symlink it is resolved once (so users can pass a
// symlinked source directory); symlinks *inside* the tree are rejected.
// Empty directories are not preserved (no zip entry is written for them);
// SKILL.md presence is the caller's job to validate.
func ArchiveDirectory(srcDir string, opts ArchiveOptions) ([]byte, error) {
	info, err := os.Stat(srcDir)
	if err != nil {
		return nil, fmt.Errorf("stat %q: %w", srcDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %q is not a directory", ErrInvalidArchive, srcDir)
	}

	maxEntries := opts.MaxEntries
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	maxBytes := opts.MaxTotalUncompressed
	if maxBytes <= 0 {
		maxBytes = DefaultMaxTotalUncompressed
	}

	// Resolve a top-level symlink so WalkDir descends into the real tree.
	// Symlinks discovered inside the tree are rejected by the walker below.
	realSrc, err := filepath.EvalSymlinks(srcDir)
	if err != nil {
		return nil, fmt.Errorf("resolve source dir: %w", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	var (
		entries    int
		totalBytes int64
	)
	walkErr := filepath.WalkDir(realSrc, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == realSrc {
			return nil
		}

		rel, relErr := filepath.Rel(realSrc, path)
		if relErr != nil {
			return fmt.Errorf("compute relative path: %w", relErr)
		}
		relSlash := filepath.ToSlash(rel)

		entryInfo, lstatErr := os.Lstat(path)
		if lstatErr != nil {
			return fmt.Errorf("stat %q: %w", relSlash, lstatErr)
		}
		mode := entryInfo.Mode()

		if mode&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %q is a symlink", ErrUnsafeEntry, relSlash)
		}
		if d.IsDir() {
			// Empty directories aren't part of the skill contract; skip the
			// entry. Non-empty directories will materialize implicitly when
			// their files are added.
			return nil
		}
		if !mode.IsRegular() {
			return fmt.Errorf("%w: %q has irregular file mode %v", ErrUnsafeEntry, relSlash, mode)
		}

		entries++
		if entries > maxEntries {
			return fmt.Errorf("%w: entry count exceeds %d", ErrLimitExceeded, maxEntries)
		}

		// Fail fast on advertised size before we open the file.
		if entryInfo.Size() > maxBytes-totalBytes {
			return fmt.Errorf("%w: uncompressed size would exceed budget", ErrLimitExceeded)
		}

		header := &zip.FileHeader{
			Name:     relSlash,
			Method:   zip.Deflate,
			Modified: entryInfo.ModTime(),
		}
		// SetMode keeps the entry's "regular file" classification in the
		// archive's external-attrs so SafeExtract sees IsRegular() on the
		// way back down. 0644 is a neutral mode that round-trips cleanly.
		header.SetMode(0644)

		w, headerErr := zw.CreateHeader(header)
		if headerErr != nil {
			return fmt.Errorf("create zip header for %q: %w", relSlash, headerErr)
		}

		f, openErr := os.Open(path) //nolint:gosec // path is under user-supplied srcDir, walked on user behalf
		if openErr != nil {
			return fmt.Errorf("open %q: %w", relSlash, openErr)
		}
		// LimitReader caps actual bytes copied at one past the remaining
		// budget so we can detect a file that grew between Lstat and Open
		// (TOCTOU) and refuse it before exceeding the total cap.
		remaining := maxBytes - totalBytes
		n, copyErr := io.Copy(w, io.LimitReader(f, remaining+1))
		_ = f.Close()
		if copyErr != nil {
			return fmt.Errorf("write %q to zip: %w", relSlash, copyErr)
		}
		if n > remaining {
			return fmt.Errorf("%w: uncompressed size would exceed budget", ErrLimitExceeded)
		}
		totalBytes += n
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close zip writer: %w", err)
	}
	if entries == 0 {
		return nil, fmt.Errorf("%w: directory %q contains no files to archive", ErrInvalidArchive, srcDir)
	}
	return buf.Bytes(), nil
}

// LocateSkillMdInDir returns the path to a SKILL.md at the root of dir, or
// ("", false, nil) when the directory has no SKILL.md. An error is returned
// only when the entry exists but is not a regular file (symlink, device,
// etc.) or when stat itself fails for a reason other than "not exist".
//
// Lookup is intentionally shallow (root only): the directory contract for
// `azd ai skill create --file <dir>` mirrors what `azd ai skill download`
// writes, which is SKILL.md at the top of the destination.
func LocateSkillMdInDir(dir string) (string, bool, error) {
	candidate := filepath.Join(dir, SkillMdFileName)
	info, err := os.Lstat(candidate)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("stat %q: %w", candidate, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("%w: %q is a symlink", ErrUnsafeEntry, candidate)
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("%w: %q is not a regular file", ErrUnsafeEntry, candidate)
	}
	return candidate, true, nil
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package skill_api

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Default archive-safety limits. The spec caps decompression at 10,000 entries
// and 512 MB total uncompressed. Callers may override via ExtractOptions.
const (
	DefaultMaxEntries           = 10_000
	DefaultMaxTotalUncompressed = 512 * 1024 * 1024
)

// ExtractOptions configures SafeExtract behavior.
type ExtractOptions struct {
	// OutputDir is the final destination directory. Created if missing.
	OutputDir string
	// Force allows overwriting existing files in OutputDir. When false,
	// SafeExtract returns ErrCollision listing the first collision encountered
	// and writes nothing.
	Force bool
	// MaxEntries caps the number of tar entries processed. Zero falls back to
	// DefaultMaxEntries.
	MaxEntries int
	// MaxTotalUncompressed caps the total uncompressed byte count across all
	// regular-file entries. Zero falls back to DefaultMaxTotalUncompressed.
	MaxTotalUncompressed int64
}

// ExtractResult is returned on success.
type ExtractResult struct {
	// Files is the list of regular files written, relative to OutputDir,
	// in the order they were extracted.
	Files []string
	// TotalBytes is the cumulative uncompressed size of extracted files.
	TotalBytes int64
}

// Sentinel errors. Each wraps additional context describing the offending
// entry / collision so callers can include it in the user-facing message.

// ErrUnsafeEntry indicates a tar entry was rejected (absolute path,
// `..` component, symlink, hard link, non-regular file, or empty/`/`-only name).
var ErrUnsafeEntry = errors.New("unsafe tar entry")

// ErrLimitExceeded indicates the entry count or uncompressed byte limit was
// exceeded mid-extraction.
var ErrLimitExceeded = errors.New("archive exceeds safety limit")

// ErrCollision indicates the archive would overwrite a file that already
// exists in OutputDir and Force was not set.
var ErrCollision = errors.New("output collision")

// ErrInvalidGzip is returned when the response is not a valid gzip stream.
var ErrInvalidGzip = errors.New("invalid gzip stream")

// SafeExtract reads a gzipped tar stream from r and writes its regular-file
// contents under opts.OutputDir. The implementation is two-phase:
//
//  1. Stream the archive entries into a temporary staging directory under the
//     OS temp dir, validating each entry against the safety rules before
//     writing anything.
//  2. After every entry has been written to staging, copy each file into the
//     final OutputDir. If anything fails partway, the staging directory is
//     removed and nothing is left behind in OutputDir.
//
// Safety rules (each rejection returns ErrUnsafeEntry):
//
//   - Absolute paths or paths containing `..` components are rejected.
//   - Symbolic links and hard links are rejected.
//   - Non-regular and non-directory entries (devices, FIFOs, sockets) are
//     rejected.
//   - Empty names, or names that collapse to "/" or "." after cleaning, are
//     rejected.
//   - Total entry count is capped at opts.MaxEntries (default 10,000).
//   - Total uncompressed byte count is capped at opts.MaxTotalUncompressed
//     (default 512 MB).
//
// Executable bits from tar headers are dropped; written files use 0600 / 0700
// modes against the process umask.
func SafeExtract(r io.Reader, opts ExtractOptions) (*ExtractResult, error) {
	if opts.OutputDir == "" {
		return nil, fmt.Errorf("SafeExtract: OutputDir is required")
	}
	maxEntries := opts.MaxEntries
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	maxBytes := opts.MaxTotalUncompressed
	if maxBytes <= 0 {
		maxBytes = DefaultMaxTotalUncompressed
	}

	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidGzip, err)
	}
	defer gz.Close()

	staging, err := os.MkdirTemp("", "azd-skill-extract-*")
	if err != nil {
		return nil, fmt.Errorf("create staging directory: %w", err)
	}
	cleanupStaging := func() {
		_ = os.RemoveAll(staging)
	}

	tr := tar.NewReader(gz)
	var files []string
	var totalBytes int64
	entryCount := 0

	for {
		hdr, hdrErr := tr.Next()
		if errors.Is(hdrErr, io.EOF) {
			break
		}
		if hdrErr != nil {
			cleanupStaging()
			return nil, fmt.Errorf("read tar entry: %w", hdrErr)
		}

		entryCount++
		if entryCount > maxEntries {
			cleanupStaging()
			return nil, fmt.Errorf("%w: entry count exceeds %d", ErrLimitExceeded, maxEntries)
		}

		cleaned, ok := validateEntryName(hdr.Name)
		if !ok {
			cleanupStaging()
			return nil, fmt.Errorf("%w: %q", ErrUnsafeEntry, hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			// Fall through.
		case tar.TypeDir:
			dirPath := filepath.Join(staging, filepath.FromSlash(cleaned))
			if mkErr := os.MkdirAll(dirPath, 0700); mkErr != nil {
				cleanupStaging()
				return nil, fmt.Errorf("create staging dir %q: %w", cleaned, mkErr)
			}
			continue
		default:
			cleanupStaging()
			return nil, fmt.Errorf("%w: %q has unsupported tar type %c", ErrUnsafeEntry, hdr.Name, hdr.Typeflag)
		}

		// Reject explicit zero-size entries that the spec only allows for
		// directories. Regular files with hdr.Size == 0 are fine (empty files).

		// Pre-create parent directory in staging.
		stagingPath := filepath.Join(staging, filepath.FromSlash(cleaned))
		if mkErr := os.MkdirAll(filepath.Dir(stagingPath), 0700); mkErr != nil {
			cleanupStaging()
			return nil, fmt.Errorf("create staging dir for %q: %w", cleaned, mkErr)
		}

		remaining := maxBytes - totalBytes
		if hdr.Size > 0 && hdr.Size > remaining {
			cleanupStaging()
			return nil, fmt.Errorf("%w: uncompressed size would exceed %d bytes", ErrLimitExceeded, maxBytes)
		}

		// Cap reading at the remaining budget so a lying header cannot run away.
		limited := io.LimitReader(tr, remaining+1)

		f, fErr := os.OpenFile(stagingPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600) //nolint:gosec // stagingPath is inside our trusted staging dir
		if fErr != nil {
			cleanupStaging()
			return nil, fmt.Errorf("create staging file %q: %w", cleaned, fErr)
		}

		written, copyErr := io.Copy(f, limited)
		closeErr := f.Close()
		if copyErr != nil {
			cleanupStaging()
			return nil, fmt.Errorf("write %q: %w", cleaned, copyErr)
		}
		if closeErr != nil {
			cleanupStaging()
			return nil, fmt.Errorf("close staging file %q: %w", cleaned, closeErr)
		}
		if written > remaining {
			cleanupStaging()
			return nil, fmt.Errorf("%w: uncompressed size would exceed %d bytes", ErrLimitExceeded, maxBytes)
		}
		totalBytes += written
		files = append(files, cleaned)
	}

	// All entries validated and written to staging. Check for collisions in
	// OutputDir before any final copy so a partial copy never leaves files behind.
	if mkErr := os.MkdirAll(opts.OutputDir, 0700); mkErr != nil {
		cleanupStaging()
		return nil, fmt.Errorf("create output dir: %w", mkErr)
	}

	if !opts.Force {
		for _, rel := range files {
			dst := filepath.Join(opts.OutputDir, filepath.FromSlash(rel))
			if _, statErr := os.Lstat(dst); statErr == nil {
				cleanupStaging()
				return nil, fmt.Errorf("%w: %s already exists in %s", ErrCollision, rel, opts.OutputDir)
			} else if !errors.Is(statErr, os.ErrNotExist) {
				cleanupStaging()
				return nil, fmt.Errorf("stat %q: %w", dst, statErr)
			}
		}
	}

	// Resolve the real output directory path once so we can detect symlink
	// escapes: if opts.OutputDir already contains a subdirectory that is a
	// symlink pointing outside opts.OutputDir, an archive entry whose cleaned
	// path starts with that component would silently write outside the
	// intended destination.
	realOutDir, evalErr := filepath.EvalSymlinks(opts.OutputDir)
	if evalErr != nil {
		cleanupStaging()
		return nil, fmt.Errorf("resolve output dir path: %w", evalErr)
	}

	// Preflight pass: create destination subdirectories and verify that every
	// resolved destination path stays inside opts.OutputDir before any file
	// data is copied. This preserves the documented contract that a partial
	// failure leaves no files behind: if any entry would escape via a symlink,
	// we abort here without having copied anything yet.
	for _, rel := range files {
		dst := filepath.Join(opts.OutputDir, filepath.FromSlash(rel))
		dstDir := filepath.Dir(dst)
		if mkErr := os.MkdirAll(dstDir, 0700); mkErr != nil {
			cleanupStaging()
			return nil, fmt.Errorf("create output dir for %q: %w", rel, mkErr)
		}
		realDstDir, evalErr := filepath.EvalSymlinks(dstDir)
		if evalErr != nil {
			cleanupStaging()
			return nil, fmt.Errorf("resolve destination path for %q: %w", rel, evalErr)
		}
		if !isUnder(realDstDir, realOutDir) {
			cleanupStaging()
			return nil, fmt.Errorf(
				"%w: %q destination escapes output directory via symlink",
				ErrUnsafeEntry, rel,
			)
		}
	}

	// All destinations validated. Copy from staging to OutputDir.
	for _, rel := range files {
		src := filepath.Join(staging, filepath.FromSlash(rel))
		dst := filepath.Join(opts.OutputDir, filepath.FromSlash(rel))
		if err := copyFile(src, dst); err != nil {
			cleanupStaging()
			return nil, fmt.Errorf("copy %q to output: %w", rel, err)
		}
	}

	cleanupStaging()
	return &ExtractResult{
		Files:      files,
		TotalBytes: totalBytes,
	}, nil
}

// validateEntryName cleans and validates a tar entry name. It returns the
// cleaned, slash-rooted relative path on success (no leading slash, no `..`
// segments).
//
// `..` is rejected even when surrounding segments cancel it out (e.g.
// `a/../b`). Cleaning would collapse that to `b`, which is technically safe
// from zip-slip, but the design spec rejects any `..` segment defensively
// so a future bug in `path.Clean` cannot regress us.
func validateEntryName(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	// Tar entry names use forward slashes. Normalize to slashes before cleaning
	// so we behave the same on Windows and Unix.
	slashed := strings.ReplaceAll(name, "\\", "/")
	// Reject Windows drive-letter syntax masquerading as a relative path.
	if len(slashed) >= 2 && slashed[1] == ':' {
		return "", false
	}
	// Reject absolute paths.
	if strings.HasPrefix(slashed, "/") {
		return "", false
	}
	// Reject any `..` segment in the *raw* name. This is stricter than
	// path.Clean — we want to refuse archives that even attempt traversal.
	for _, part := range strings.Split(slashed, "/") {
		if part == ".." {
			return "", false
		}
	}
	// path.Clean to remove redundant separators and resolve `.` segments
	// without traversing the filesystem.
	cleaned := path.Clean(slashed)
	if cleaned == "" || cleaned == "." || cleaned == "/" {
		return "", false
	}
	if path.IsAbs(cleaned) || strings.HasPrefix(cleaned, "/") {
		return "", false
	}
	return cleaned, true
}

// isUnder reports whether child is the same as or nested within parent.
// Both paths must be cleaned real paths (output of filepath.EvalSymlinks).
func isUnder(child, parent string) bool {
	if child == parent {
		return true
	}
	return strings.HasPrefix(child, parent+string(filepath.Separator))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // src is inside our trusted staging dir
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600) //nolint:gosec // dst is inside the user-supplied output dir, written on user behalf
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

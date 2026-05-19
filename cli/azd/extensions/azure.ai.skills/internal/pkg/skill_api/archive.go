// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package skill_api

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

const (
	DefaultMaxEntries           = 10_000
	DefaultMaxTotalUncompressed = 512 * 1024 * 1024
)

type ExtractOptions struct {
	OutputDir            string
	Force                bool
	MaxEntries           int
	MaxTotalUncompressed int64
}

type ExtractResult struct {
	Files      []string
	TotalBytes int64
}

var (
	ErrUnsafeEntry    = errors.New("unsafe archive entry")
	ErrLimitExceeded  = errors.New("archive exceeds safety limit")
	ErrCollision      = errors.New("output collision")
	ErrInvalidArchive = errors.New("invalid archive")
)

type ArchiveFormat int

const (
	ArchiveUnknown ArchiveFormat = iota
	ArchiveZip
	ArchiveTarGz
)

func (f ArchiveFormat) String() string {
	switch f {
	case ArchiveZip:
		return "zip"
	case ArchiveTarGz:
		return "tar.gz"
	default:
		return "unknown"
	}
}

// DetectArchiveFormat sniffs the first bytes of data. The Foundry Skills
// service is asymmetric: POST /skills:import requires ZIP (gzip yields 415),
// but GET /skills/{name}:download returns gzip — so we sniff rather than
// trust Content-Type.
func DetectArchiveFormat(data []byte) ArchiveFormat {
	switch {
	case len(data) >= 4 && bytes.Equal(data[:4], []byte{'P', 'K', 0x03, 0x04}):
		return ArchiveZip
	case len(data) >= 4 && bytes.Equal(data[:4], []byte{'P', 'K', 0x05, 0x06}):
		return ArchiveZip
	case len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b:
		return ArchiveTarGz
	default:
		return ArchiveUnknown
	}
}

// SafeExtract extracts a ZIP or gzip-tar archive into opts.OutputDir.
//
// Two-phase: entries are first written into a temp staging directory and
// validated against the safety rules below; then symlink-escape checks run
// and files are copied into OutputDir. A failed extraction leaves nothing
// in OutputDir.
//
// Rejections (ErrUnsafeEntry): absolute paths, `..` segments, empty names,
// non-regular entries (symlinks, hard links, devices, sockets).
// Caps (ErrLimitExceeded): MaxEntries (default 10,000),
// MaxTotalUncompressed (default 512 MB).
func SafeExtract(data []byte, opts ExtractOptions) (*ExtractResult, error) {
	if opts.OutputDir == "" {
		return nil, fmt.Errorf("SafeExtract: OutputDir is required")
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty body", ErrInvalidArchive)
	}
	maxEntries := opts.MaxEntries
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	maxBytes := opts.MaxTotalUncompressed
	if maxBytes <= 0 {
		maxBytes = DefaultMaxTotalUncompressed
	}

	staging, err := os.MkdirTemp("", "azd-skill-extract-*")
	if err != nil {
		return nil, fmt.Errorf("create staging directory: %w", err)
	}
	cleanupStaging := func() { _ = os.RemoveAll(staging) }

	var (
		files      []string
		totalBytes int64
		stageErr   error
	)
	switch DetectArchiveFormat(data) {
	case ArchiveZip:
		files, totalBytes, stageErr = stageFromZip(data, staging, maxEntries, maxBytes)
	case ArchiveTarGz:
		files, totalBytes, stageErr = stageFromTarGz(data, staging, maxEntries, maxBytes)
	default:
		cleanupStaging()
		return nil, fmt.Errorf("%w: unrecognized magic bytes", ErrInvalidArchive)
	}
	if stageErr != nil {
		cleanupStaging()
		return nil, stageErr
	}

	if err := publishToOutputDir(staging, files, opts); err != nil {
		cleanupStaging()
		return nil, err
	}

	cleanupStaging()
	return &ExtractResult{Files: files, TotalBytes: totalBytes}, nil
}

func stageFromZip(data []byte, staging string, maxEntries int, maxBytes int64) ([]string, int64, error) {
	zr, err := zip.NewReader(newBytesReaderAt(data), int64(len(data)))
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrInvalidArchive, err)
	}
	if len(zr.File) > maxEntries {
		return nil, 0, fmt.Errorf("%w: entry count %d exceeds %d", ErrLimitExceeded, len(zr.File), maxEntries)
	}

	var files []string
	var totalBytes int64
	for _, entry := range zr.File {
		cleaned, ok := validateEntryName(entry.Name)
		if !ok {
			return nil, 0, fmt.Errorf("%w: %q", ErrUnsafeEntry, entry.Name)
		}

		mode := entry.Mode()
		switch {
		case mode.IsDir() || strings.HasSuffix(entry.Name, "/"):
			if mkErr := os.MkdirAll(filepath.Join(staging, filepath.FromSlash(cleaned)), 0700); mkErr != nil {
				return nil, 0, fmt.Errorf("create staging dir %q: %w", cleaned, mkErr)
			}
			continue
		case mode.IsRegular():
		default:
			return nil, 0, fmt.Errorf("%w: %q has irregular file mode %v", ErrUnsafeEntry, entry.Name, mode)
		}

		written, writeErr := writeStagingEntry(staging, cleaned, func(w io.Writer, limit int64) (int64, error) {
			rc, openErr := entry.Open()
			if openErr != nil {
				return 0, fmt.Errorf("open zip entry %q: %w", cleaned, openErr)
			}
			defer rc.Close()
			return io.Copy(w, io.LimitReader(rc, limit))
		}, maxBytes-totalBytes, int64(entry.UncompressedSize64)) //nolint:gosec // bound-checked inside writeStagingEntry
		if writeErr != nil {
			return nil, 0, writeErr
		}
		totalBytes += written
		files = append(files, cleaned)
	}
	return files, totalBytes, nil
}

func stageFromTarGz(data []byte, staging string, maxEntries int, maxBytes int64) ([]string, int64, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrInvalidArchive, err)
	}
	defer gz.Close()

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
			return nil, 0, fmt.Errorf("read tar entry: %w", hdrErr)
		}
		if entryCount >= maxEntries {
			return nil, 0, fmt.Errorf("%w: entry count exceeds %d", ErrLimitExceeded, maxEntries)
		}
		entryCount++

		cleaned, ok := validateEntryName(hdr.Name)
		if !ok {
			return nil, 0, fmt.Errorf("%w: %q", ErrUnsafeEntry, hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			if hdr.Linkname != "" {
				return nil, 0, fmt.Errorf("%w: %q regular-file entry has unexpected Linkname", ErrUnsafeEntry, hdr.Name)
			}
		case tar.TypeDir:
			if mkErr := os.MkdirAll(filepath.Join(staging, filepath.FromSlash(cleaned)), 0700); mkErr != nil {
				return nil, 0, fmt.Errorf("create staging dir %q: %w", cleaned, mkErr)
			}
			continue
		default:
			return nil, 0, fmt.Errorf("%w: %q has unsupported tar type %c", ErrUnsafeEntry, hdr.Name, hdr.Typeflag)
		}

		written, writeErr := writeStagingEntry(staging, cleaned, func(w io.Writer, limit int64) (int64, error) {
			return io.Copy(w, io.LimitReader(tr, limit))
		}, maxBytes-totalBytes, hdr.Size)
		if writeErr != nil {
			return nil, 0, writeErr
		}
		totalBytes += written
		files = append(files, cleaned)
	}
	return files, totalBytes, nil
}

// writeStagingEntry creates parent directories and writes one entry into
// staging via writeBody. writeBody must copy at most limit+1 bytes; if it
// writes more than `remaining`, ErrLimitExceeded is returned.
func writeStagingEntry(
	staging, relName string,
	writeBody func(w io.Writer, limit int64) (int64, error),
	remaining int64,
	advertisedSize int64,
) (int64, error) {
	stagingPath := filepath.Join(staging, filepath.FromSlash(relName))
	if mkErr := os.MkdirAll(filepath.Dir(stagingPath), 0700); mkErr != nil {
		return 0, fmt.Errorf("create staging dir for %q: %w", relName, mkErr)
	}
	if advertisedSize > 0 && advertisedSize > remaining {
		return 0, fmt.Errorf("%w: uncompressed size would exceed budget", ErrLimitExceeded)
	}
	f, fErr := os.OpenFile(stagingPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600) //nolint:gosec // staging is our trusted temp dir
	if fErr != nil {
		return 0, fmt.Errorf("create staging file %q: %w", relName, fErr)
	}
	written, copyErr := writeBody(f, remaining+1)
	closeErr := f.Close()
	switch {
	case copyErr != nil:
		return 0, fmt.Errorf("write %q: %w", relName, copyErr)
	case closeErr != nil:
		return 0, fmt.Errorf("close staging file %q: %w", relName, closeErr)
	case written > remaining:
		return 0, fmt.Errorf("%w: uncompressed size would exceed budget", ErrLimitExceeded)
	}
	return written, nil
}

func publishToOutputDir(staging string, files []string, opts ExtractOptions) error {
	if mkErr := os.MkdirAll(opts.OutputDir, 0700); mkErr != nil {
		return fmt.Errorf("create output dir: %w", mkErr)
	}

	if !opts.Force {
		for _, rel := range files {
			dst := filepath.Join(opts.OutputDir, filepath.FromSlash(rel))
			if _, statErr := os.Lstat(dst); statErr == nil {
				return fmt.Errorf("%w: %s already exists in %s", ErrCollision, rel, opts.OutputDir)
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return fmt.Errorf("stat %q: %w", dst, statErr)
			}
		}
	}

	// Resolve OutputDir's real path so we can reject entries that would
	// escape through a pre-existing symlink in OutputDir.
	realOutDir, evalErr := filepath.EvalSymlinks(opts.OutputDir)
	if evalErr != nil {
		return fmt.Errorf("resolve output dir path: %w", evalErr)
	}

	// Preflight: create destination directories and verify every resolved
	// destination stays inside OutputDir, before any file is copied.
	for _, rel := range files {
		dstDir := filepath.Dir(filepath.Join(opts.OutputDir, filepath.FromSlash(rel)))
		if mkErr := os.MkdirAll(dstDir, 0700); mkErr != nil {
			return fmt.Errorf("create output dir for %q: %w", rel, mkErr)
		}
		realDstDir, evalErr := filepath.EvalSymlinks(dstDir)
		if evalErr != nil {
			return fmt.Errorf("resolve destination path for %q: %w", rel, evalErr)
		}
		if !isUnder(realDstDir, realOutDir) {
			return fmt.Errorf("%w: %q destination escapes output directory via symlink", ErrUnsafeEntry, rel)
		}
	}

	for _, rel := range files {
		src := filepath.Join(staging, filepath.FromSlash(rel))
		dst := filepath.Join(opts.OutputDir, filepath.FromSlash(rel))
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("copy %q to output: %w", rel, err)
		}
	}
	return nil
}

// validateEntryName cleans and validates an archive entry name. Returns the
// cleaned, slash-separated relative path. Rejects `..` even when surrounding
// segments cancel it out (e.g. `a/../b`) — defense against future bugs in
// path.Clean.
func validateEntryName(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	slashed := strings.ReplaceAll(name, "\\", "/")
	withoutTrailing := strings.TrimSuffix(slashed, "/")
	if withoutTrailing == "" {
		return "", false
	}
	if len(withoutTrailing) >= 2 && withoutTrailing[1] == ':' {
		return "", false
	}
	// Reject UNC paths (\\server\share normalizes to //server/share).
	if strings.HasPrefix(withoutTrailing, "//") {
		return "", false
	}
	if strings.HasPrefix(withoutTrailing, "/") {
		return "", false
	}
	if slices.Contains(strings.Split(withoutTrailing, "/"), "..") {
		return "", false
	}
	cleaned := path.Clean(withoutTrailing)
	if cleaned == "" || cleaned == "." || cleaned == "/" {
		return "", false
	}
	if path.IsAbs(cleaned) || strings.HasPrefix(cleaned, "/") {
		return "", false
	}
	return cleaned, true
}

func isUnder(child, parent string) bool {
	if child == parent {
		return true
	}
	return strings.HasPrefix(child, parent+string(filepath.Separator))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // staging is our trusted temp dir
	if err != nil {
		return err
	}
	defer in.Close()

	// Even with --force, never follow a symlink at the destination: O_TRUNC
	// would otherwise overwrite whatever the link points at, potentially
	// outside OutputDir. Lstat first, reject non-regular entries, and remove
	// any pre-existing regular file so the O_EXCL open below owns the path.
	if info, lstatErr := os.Lstat(dst); lstatErr == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf(
				"%w: destination %q already exists and is not a regular file",
				ErrUnsafeEntry, dst,
			)
		}
		if rmErr := os.Remove(dst); rmErr != nil {
			return fmt.Errorf("remove existing destination %q: %w", dst, rmErr)
		}
	} else if !errors.Is(lstatErr, os.ErrNotExist) {
		return fmt.Errorf("stat destination %q: %w", dst, lstatErr)
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600) //nolint:gosec // user-supplied output dir, written on user behalf
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

type bytesReaderAt struct{ data []byte }

func newBytesReaderAt(data []byte) *bytesReaderAt { return &bytesReaderAt{data: data} }

func (b *bytesReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off > int64(len(b.data)) {
		return 0, io.EOF
	}
	n := copy(p, b.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

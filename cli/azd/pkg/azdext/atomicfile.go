// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// ---------------------------------------------------------------------------
// Atomic file operations
// ---------------------------------------------------------------------------

// WriteFileAtomic writes data to the named file atomically. It writes to a
// temporary file in the same directory as path and renames it into place. This
// ensures that readers never see a partially-written file and that the
// operation is crash-safe on filesystems that support atomic rename (ext4,
// APFS, NTFS).
//
// Platform behavior:
//   - Unix: os.Rename is atomic within the same filesystem.
//   - Windows: os.Rename replaces the target if it exists (Go 1.16+). On
//     older Go runtimes or cross-device moves, the operation may fail.
//     WriteFileAtomic always places the temp file in the same directory to
//     avoid cross-device issues.
//
// The file is created with the specified permissions. If the target already
// exists its permissions are preserved unless perm is explicitly non-zero.
//
// Returns an error if the directory does not exist, the temp file cannot be
// created, data cannot be written, or the rename fails.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)

	// Validate that the target directory exists.
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("azdext.WriteFileAtomic: target directory: %w", err)
	}

	// If perm is zero and the target exists, preserve existing permissions.
	if perm == 0 {
		if fi, err := os.Stat(path); err == nil {
			perm = fi.Mode().Perm()
		} else {
			perm = 0o644
		}
	}

	// Create temp file in the same directory (same filesystem = atomic rename).
	tmp, err := os.CreateTemp(dir, ".azdext-atomic-*")
	if err != nil {
		return fmt.Errorf("azdext.WriteFileAtomic: create temp: %w", err)
	}
	tmpPath := tmp.Name()

	// Ensure cleanup on any failure path.
	success := false
	defer func() {
		if !success {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	// Write data and sync to disk.
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("azdext.WriteFileAtomic: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("azdext.WriteFileAtomic: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("azdext.WriteFileAtomic: close: %w", err)
	}

	// Set permissions on temp file before rename.
	//nolint:gosec // G703: tmpPath is constructed internally
	if err := os.Chmod(tmpPath, perm); err != nil {
		return fmt.Errorf("azdext.WriteFileAtomic: chmod: %w", err)
	}

	// Atomic rename into place.
	if err := osutil.Rename(context.Background(), tmpPath, path); err != nil {
		return fmt.Errorf("azdext.WriteFileAtomic: rename: %w", err)
	}

	success = true
	return nil
}

// CopyFileAtomic copies src to dst atomically using the write-temp-rename
// pattern. The destination file is never in a partially-written state.
//
// Platform behavior: see [WriteFileAtomic].
//
// If perm is zero, the source file's permissions are used.
func CopyFileAtomic(src, dst string, perm os.FileMode) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("azdext.CopyFileAtomic: open source: %w", err)
	}
	defer srcFile.Close()

	// Determine permissions.
	if perm == 0 {
		if fi, err := srcFile.Stat(); err == nil {
			perm = fi.Mode().Perm()
		} else {
			perm = 0o644
		}
	}

	dir := filepath.Dir(dst)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("azdext.CopyFileAtomic: target directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".azdext-atomic-*")
	if err != nil {
		return fmt.Errorf("azdext.CopyFileAtomic: create temp: %w", err)
	}
	tmpPath := tmp.Name()

	success := false
	defer func() {
		if !success {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmp, srcFile); err != nil {
		return fmt.Errorf("azdext.CopyFileAtomic: copy source: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("azdext.CopyFileAtomic: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("azdext.CopyFileAtomic: close: %w", err)
	}
	//nolint:gosec // G703: tmpPath is constructed internally
	if err := os.Chmod(tmpPath, perm); err != nil {
		return fmt.Errorf("azdext.CopyFileAtomic: chmod: %w", err)
	}
	if err := osutil.Rename(context.Background(), tmpPath, dst); err != nil {
		return fmt.Errorf("azdext.CopyFileAtomic: rename: %w", err)
	}

	success = true
	return nil
}

// BackupFile creates a backup copy of path at path+suffix using atomic copy.
// If the source file does not exist, it returns nil (no backup needed).
//
// The default suffix is ".bak" if suffix is empty.
//
// Returns the backup path on success, or an error if the copy fails.
func BackupFile(path, suffix string) (string, error) {
	if suffix == "" {
		suffix = ".bak"
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", nil // Nothing to back up.
	}

	backupPath := path + suffix
	if err := CopyFileAtomic(path, backupPath, 0); err != nil {
		return "", fmt.Errorf("azdext.BackupFile: %w", err)
	}

	return backupPath, nil
}

// EnsureDir creates directory dir and any necessary parents with the given
// permissions. If the directory already exists, EnsureDir is a no-op and
// returns nil.
//
// This is a convenience wrapper around [os.MkdirAll] with an explicit error
// prefix for diagnostics.
func EnsureDir(dir string, perm os.FileMode) error {
	if perm == 0 {
		perm = 0o755
	}
	if err := os.MkdirAll(dir, perm); err != nil {
		return fmt.Errorf("azdext.EnsureDir: %w", err)
	}
	return nil
}

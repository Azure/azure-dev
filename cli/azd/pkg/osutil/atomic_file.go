// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path using a temporary file in the same directory
// and atomically renames it into place.
func WriteFileAtomic(ctx context.Context, path string, data []byte, perm os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("stating target directory: %w", err)
	}

	if perm == 0 {
		if info, err := os.Stat(path); err == nil {
			perm = info.Mode().Perm()
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stating target file: %w", err)
		} else {
			perm = PermissionFile
		}
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	//nolint:gosec // tmpPath is created in the trusted target directory.
	if err := os.Chmod(tmpPath, perm); err != nil {
		return fmt.Errorf("setting temporary file permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("writing temporary file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("syncing temporary file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temporary file: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := Rename(ctx, tmpPath, path); err != nil {
		return fmt.Errorf("replacing target file: %w", err)
	}

	removeTemp = false
	return nil
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// isSubpath returns true if child is inside or equal to parent.
func isSubpath(child, parent string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isSamePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// copyDirectory recursively copies all files and directories from src to dst.
func copyDirectory(src, dst string) error {
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("resolving absolute source path %s: %w", src, err)
	}
	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("resolving absolute destination path %s: %w", dst, err)
	}

	// No-op: already in the destination directory (re-init / overwrite scenario).
	if isSamePath(dstAbs, srcAbs) {
		return nil
	}

	if isSubpath(dstAbs, srcAbs) {
		return fmt.Errorf("refusing to copy directory '%s' into its own subtree '%s'", srcAbs, dstAbs)
	}

	return filepath.WalkDir(srcAbs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Calculate the destination path
		relPath, err := filepath.Rel(srcAbs, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dstAbs, relPath)

		if d.IsDir() {
			// Create directory and continue processing its contents
			//nolint:gosec // copied project directories should remain readable/traversable
			return os.MkdirAll(dstPath, 0755)
		}

		// Copy file
		return copyFile(path, dstPath)
	})
}

// copyFile copies a single file from src to dst.
func copyFile(src, dst string) error {
	// Create the destination directory if it doesn't exist
	//nolint:gosec // copied project directories should remain readable/traversable
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	// Open source file
	//nolint:gosec // source path is computed from validated copy traversal
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		_ = srcFile.Close()
	}()

	// Create destination file
	//nolint:gosec // destination path is computed from validated copy traversal
	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		_ = dstFile.Close()
	}()

	// Copy file contents
	_, err = srcFile.WriteTo(dstFile)
	return err
}

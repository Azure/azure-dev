// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package service

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// MaxHashVersionLength is the maximum length for a dataset version string.
	// Azure ML dataset versions have a 50-char limit; we use 49 to stay safe.
	MaxHashVersionLength = 49
)

// ComputeDirectoryHash computes a deterministic SHA-256 hash of a directory's contents.
// The hash covers every file's relative path (normalized to forward slashes) and its contents,
// so any change to filenames, directory structure, or file data produces a different hash.
//
// Files are sorted lexicographically by relative path to ensure determinism across platforms.
// The returned string is the full hex-encoded SHA-256 hash (64 characters).
// Use TruncateHashVersion() to shorten it for use as a dataset version.
func ComputeDirectoryHash(dirPath string) (string, error) {
	absDir, err := filepath.Abs(dirPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve directory path: %w", err)
	}

	// Collect all regular files with their relative paths
	type fileEntry struct {
		relPath string
		absPath string
	}
	var files []fileEntry

	err = filepath.Walk(absDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Skip directories — we only hash file contents and paths
		if info.IsDir() {
			return nil
		}
		// Skip symlinks — only regular files
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		rel, err := filepath.Rel(absDir, path)
		if err != nil {
			return fmt.Errorf("failed to compute relative path for %s: %w", path, err)
		}
		// Normalize to forward slashes for cross-platform determinism
		rel = strings.ReplaceAll(rel, "\\", "/")
		files = append(files, fileEntry{relPath: rel, absPath: path})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to walk directory %s: %w", dirPath, err)
	}

	// Sort by relative path for deterministic ordering
	sort.Slice(files, func(i, j int) bool {
		return files[i].relPath < files[j].relPath
	})

	// Hash all file paths and contents into a single SHA-256 digest
	h := sha256.New()
	for _, f := range files {
		// Write the relative path as a separator/identifier
		if _, err := fmt.Fprintf(h, "file:%s\n", f.relPath); err != nil {
			return "", fmt.Errorf("failed to hash path %s: %w", f.relPath, err)
		}

		// Stream file contents into the hash
		file, err := os.Open(f.absPath)
		if err != nil {
			return "", fmt.Errorf("failed to open %s: %w", f.absPath, err)
		}
		if _, err := io.Copy(h, file); err != nil {
			file.Close()
			return "", fmt.Errorf("failed to hash contents of %s: %w", f.relPath, err)
		}
		file.Close()
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// TruncateHashVersion truncates a full hash to MaxHashVersionLength characters
// for use as a dataset version string.
func TruncateHashVersion(fullHash string) string {
	if len(fullHash) > MaxHashVersionLength {
		return fullHash[:MaxHashVersionLength]
	}
	return fullHash
}

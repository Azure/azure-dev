// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package azdignore applies .azdignore rules inside the azure.ai.agents
// extension. It mirrors the contract documented for core azd
// (cli/azd/docs/azdignore.md): the root .azdignore file uses gitignore
// syntax, only the root file is read for rules, and every .azdignore
// (root + nested) is removed from the consumer output after processing.
//
// The extension cannot import cli/azd/internal/repository, so this
// package duplicates the relevant logic. Keep behavior in sync with
// loadAzdIgnore / removeAzdIgnoredFiles in
// cli/azd/internal/repository/initializer.go.
package azdignore

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	gitignore "github.com/denormal/go-gitignore"
)

// FileName is the file template authors place at the root of a template
// to exclude files from being copied when consumers run `azd ai agent init`.
const FileName = ".azdignore"

// MaxSize caps the size of a .azdignore file at 1 MB to mirror core azd.
const MaxSize = 1 << 20

// utf8BOM is the byte order mark that some Windows editors prepend to UTF-8 files.
// It is stripped before parsing so the invisible bytes do not become part of
// the first ignore pattern.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// Load reads .azdignore from the root of dir and returns a gitignore-syntax
// matcher. Returns (nil, nil) when no .azdignore file exists. Symlinks are
// rejected to prevent reading files outside dir, and oversized files are
// rejected up front.
func Load(dir string) (gitignore.GitIgnore, error) {
	path := filepath.Join(dir, FileName)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", FileName, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file", FileName)
	}
	if info.Size() > MaxSize {
		return nil, fmt.Errorf("%s exceeds maximum size (%d bytes)", FileName, MaxSize)
	}

	f, err := os.Open(path) //nolint:gosec // path is dir + constant filename, lstat-checked above
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", FileName, err)
	}
	defer f.Close()

	// LimitReader enforces the size cap on actual bytes read, guarding
	// against TOCTOU between Lstat and Open.
	data, err := io.ReadAll(io.LimitReader(f, MaxSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", FileName, err)
	}
	if int64(len(data)) > MaxSize {
		return nil, fmt.Errorf("%s exceeds maximum size (%d bytes)", FileName, MaxSize)
	}

	data = bytes.TrimPrefix(data, utf8BOM)
	return gitignore.New(bytes.NewReader(data), dir, nil), nil
}

// Apply reads .azdignore from dir, removes every matching file or
// directory, and then strips every .azdignore file (root + nested) so
// the consumer never sees them. It is a no-op when no .azdignore is
// present at the root, except for the nested-cleanup pass which always
// runs to honor the documented contract.
func Apply(dir string) error {
	ig, err := Load(dir)
	if err != nil {
		return err
	}
	if ig == nil {
		// No root .azdignore -> still scrub any nested .azdignore files
		// to keep behavior identical to core's removeAzdIgnoredFiles
		// post-condition: consumers never see .azdignore files.
		return removeAllIgnoreFiles(dir)
	}

	// Collect paths to remove before mutating the tree so the walk and
	// the removal phases stay independent.
	var toRemove []string
	scanErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}

		match := ig.Relative(filepath.ToSlash(rel), d.IsDir())
		if match != nil && match.Ignore() {
			toRemove = append(toRemove, path)
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	if scanErr != nil {
		return fmt.Errorf("scanning for %s matches: %w", FileName, scanErr)
	}

	for _, p := range toRemove {
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("removing %s-ignored path: %w", FileName, err)
		}
	}

	return removeAllIgnoreFiles(dir)
}

// removeAllIgnoreFiles walks dir and deletes every .azdignore file.
// This includes the root file that Apply just processed and any nested
// .azdignore files the template might contain. Templates may not put
// rules in nested .azdignore files, but the contract is that consumers
// never see them in the output.
func removeAllIgnoreFiles(dir string) error {
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() && d.Name() == FileName {
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return fmt.Errorf("removing %s file: %w", FileName, removeErr)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("cleaning nested %s files: %w", FileName, err)
	}
	return nil
}

// ParseBytes builds a matcher from raw .azdignore bytes anchored at base.
// Useful for paths that have a file listing in hand (e.g., the from-code
// flow that fetches the GitHub repo tree before downloading) and need to
// pre-filter without writing the file to disk.
//
// Returns nil when data is empty after BOM stripping.
func ParseBytes(data []byte, base string) gitignore.GitIgnore {
	data = bytes.TrimPrefix(data, utf8BOM)
	if len(data) == 0 {
		return nil
	}
	return gitignore.New(bytes.NewReader(data), base, nil)
}

// Filter returns the subset of relPaths that are NOT ignored by ig. The
// .azdignore file itself is always excluded so callers do not display or
// download it. Paths must use forward slashes (the GitHub Contents API
// returns paths in that form). When ig is nil, the input is returned
// with only .azdignore stripped out.
//
// Each path is checked both as a file and against every ancestor
// directory path. This is necessary because we only have a flat file
// listing -- in a directory walk, a directory-pattern match would skip
// descendants, but here we have to detect "inside an ignored directory"
// explicitly.
func Filter(relPaths []string, ig gitignore.GitIgnore) []string {
	out := make([]string, 0, len(relPaths))
	for _, p := range relPaths {
		if isIgnoreFile(p) {
			continue
		}
		if ig != nil && IsIgnored(ig, p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// IsIgnored reports whether p is ignored by ig. The check considers the
// file itself and every ancestor directory so that patterns like
// `infra/secrets/` correctly exclude `infra/secrets/password.txt`.
// Paths must use forward slashes.
func IsIgnored(ig gitignore.GitIgnore, p string) bool {
	if ig == nil {
		return false
	}
	if match := ig.Relative(p, false); match != nil && match.Ignore() {
		return true
	}
	parts := strings.Split(p, "/")
	for i := 1; i < len(parts); i++ {
		ancestor := strings.Join(parts[:i], "/")
		if ancestor == "" {
			continue
		}
		if match := ig.Relative(ancestor, true); match != nil && match.Ignore() {
			return true
		}
	}
	return false
}

// isIgnoreFile reports whether p is the .azdignore file at any depth.
func isIgnoreFile(p string) bool {
	if p == FileName {
		return true
	}
	return strings.HasSuffix(p, "/"+FileName)
}

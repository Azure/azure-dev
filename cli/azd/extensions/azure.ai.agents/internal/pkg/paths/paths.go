// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package paths validates and resolves paths under an azd project root.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// Join resolves relativePath and elems under projectRoot.
func Join(projectRoot, relativePath string, elems ...string) (string, error) {
	return join(projectRoot, relativePath, false, elems...)
}

// JoinAllowRoot resolves relativePath and elems under projectRoot, allowing an
// empty or "." relativePath to mean the project root itself.
func JoinAllowRoot(projectRoot, relativePath string, elems ...string) (string, error) {
	return join(projectRoot, relativePath, true, elems...)
}

func join(projectRoot, relativePath string, allowRoot bool, elems ...string) (string, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return "", fmt.Errorf("project root is empty")
	}

	rootAbs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	rootAbs = filepath.Clean(rootAbs)

	cleanRel, err := cleanRelativePath(relativePath, allowRoot)
	if err != nil {
		return "", err
	}

	parts := []string{rootAbs}
	if cleanRel != "." {
		parts = append(parts, filepath.FromSlash(cleanRel))
	}
	parts = append(parts, elems...)

	resolved, err := filepath.Abs(filepath.Join(parts...))
	if err != nil {
		return "", fmt.Errorf("resolve project-relative path: %w", err)
	}
	resolved = filepath.Clean(resolved)

	if !isSubpath(resolved, rootAbs) {
		return "", fmt.Errorf("path %q escapes project root", relativePath)
	}
	if err := validateResolvedSubpath(resolved, rootAbs, relativePath); err != nil {
		return "", err
	}

	return resolved, nil
}

func cleanRelativePath(relativePath string, allowRoot bool) (string, error) {
	if relativePath == "" {
		if allowRoot {
			return ".", nil
		}
		return "", fmt.Errorf("relative path is empty")
	}
	if strings.TrimSpace(relativePath) == "" {
		return "", fmt.Errorf("relative path is empty")
	}

	normalized := strings.ReplaceAll(relativePath, "\\", "/")
	if strings.HasPrefix(normalized, "/") || hasWindowsVolume(normalized) {
		return "", fmt.Errorf("relative path %q must not be absolute", relativePath)
	}

	if slices.Contains(strings.Split(normalized, "/"), "..") {
		return "", fmt.Errorf("relative path %q must not contain '..'", relativePath)
	}

	cleaned := path.Clean(normalized)
	if cleaned == "." && !allowRoot {
		return "", fmt.Errorf("relative path %q resolves to project root", relativePath)
	}
	if strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("relative path %q escapes project root", relativePath)
	}

	return cleaned, nil
}

func hasWindowsVolume(p string) bool {
	if len(p) >= 2 && p[1] == ':' && unicode.IsLetter(rune(p[0])) {
		return true
	}
	return strings.HasPrefix(p, "//")
}

func isSubpath(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func validateResolvedSubpath(targetPath, rootPath, relativePath string) error {
	rootReal, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return fmt.Errorf("resolve project root symlinks: %w", err)
	}
	rootReal = filepath.Clean(rootReal)

	existing, err := deepestExistingPath(targetPath)
	if err != nil {
		return err
	}

	existingReal, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return fmt.Errorf("resolve project-relative path symlinks: %w", err)
	}
	existingReal = filepath.Clean(existingReal)

	if !isSubpath(existingReal, rootReal) {
		return fmt.Errorf("path %q escapes project root", relativePath)
	}

	return nil
}

func deepestExistingPath(targetPath string) (string, error) {
	current := filepath.Clean(targetPath)
	for {
		if _, err := os.Lstat(current); err == nil {
			return current, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect project-relative path: %w", err)
		}

		next := filepath.Dir(current)
		if next == current {
			return "", fmt.Errorf("project-relative path does not have an existing ancestor")
		}
		current = next
	}
}

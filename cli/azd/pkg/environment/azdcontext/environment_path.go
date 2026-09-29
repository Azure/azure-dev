// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdcontext

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrUnsafeEnvironmentPath identifies a filesystem link or a path outside the local state boundary.
var ErrUnsafeEnvironmentPath = errors.New("unsafe environment path")

// resolveExistingPath resolves the nearest existing ancestor without treating dangling
// links or permission errors as missing directories. This also supports first-time creation.
func resolveExistingPath(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(path); err == nil {
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", fmt.Errorf("environment ancestor %q is not a directory", path)
		}
		return canonicalDirectory(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	parent := filepath.Dir(path)
	if parent == path {
		return "", fmt.Errorf("resolving path %q: %w", path, os.ErrNotExist)
	}
	resolvedParent, err := resolveExistingPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(path)), nil
}

func resolveEnvironmentChild(base, name string) (string, error) {
	if !filepath.IsLocal(name) || name == "." || filepath.Base(name) != name {
		return "", fmt.Errorf("invalid environment file name %q", name)
	}
	path := filepath.Join(base, name)
	info, err := os.Lstat(path)
	if err == nil {
		if isEnvironmentLink(info) {
			return "", fmt.Errorf("%w: %q must not be a symbolic link or reparse point", ErrUnsafeEnvironmentPath, path)
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return "", fmt.Errorf("resolving environment path: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("checking environment path: %w", err)
	}

	relative, err := filepath.Rel(base, path)
	if err != nil {
		return "", fmt.Errorf("checking environment path containment: %w", err)
	}
	if relative == "." || !filepath.IsLocal(relative) {
		return "", fmt.Errorf("%w: %q resolves outside %q", ErrUnsafeEnvironmentPath, path, base)
	}
	return path, nil
}

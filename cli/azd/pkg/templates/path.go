// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package templates

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// remoteURIPrefixes lists URI scheme prefixes that identify remote git repositories.
var remoteURIPrefixes = []string{
	"git@",
	"git://",
	"ssh://",
	"file://",
	"http://",
	"https://",
}

// isRemoteURI returns true if path starts with a known remote URI prefix.
func isRemoteURI(path string) bool {
	for _, prefix := range remoteURIPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// Absolute returns an absolute template path, given a possibly relative template path. An absolute path also corresponds to
// a fully-qualified URI to a git repository.
//
// See Template.Path for more details.
func Absolute(path string) (string, error) {
	// already a remote URI, return as-is
	if isRemoteURI(path) {
		return path, nil
	}

	// Support local filesystem directories as template sources.
	// This allows using a local directory with uncommitted changes for template development.
	// Use Lstat to reject symlinks consistently with copyLocalTemplate.
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("local template directory '%s' is a symlink, which is not supported", path)
		}
		if info.IsDir() {
			absPath, err := filepath.Abs(path)
			if err != nil {
				return "", fmt.Errorf("resolving local template path: %w", err)
			}
			return absPath, nil
		}
		// Path exists but is not a directory.
		return "", fmt.Errorf("local template path '%s' exists but is not a directory", path)
	}

	// If the path looks like an explicit local path reference (".", "..", starts with ./, ../, .\, ..\,
	// or is an absolute path) but the directory wasn't found above, return a clear error
	// instead of falling through to GitHub resolution which would give a confusing error.
	if looksLikeLocalPath(path) {
		return "", fmt.Errorf("local template directory '%s' does not exist", path)
	}

	path = strings.TrimRight(path, "/")

	switch strings.Count(path, "/") {
	case 0:
		return fmt.Sprintf("https://github.com/Azure-Samples/%s", path), nil
	case 1:
		return fmt.Sprintf("https://github.com/%s", path), nil
	default:
		return "", fmt.Errorf(
			"template '%s' should be <owner>/<repo> for GitHub repositories, "+
				"or <repo> for Azure-Samples GitHub repositories", path)
	}
}

// Hyperlink returns a hyperlink to the given template path.
// If the path is cannot be resolved absolutely, it is returned as-is.
func Hyperlink(path string) string {
	url, err := Absolute(path)
	if err != nil {
		log.Printf("error: getting absolute url from template: %v", err)
		return path
	}
	return output.WithHyperlink(url, path)
}

// IsLocalPath returns true if the given resolved template path refers to a local filesystem directory
// rather than a remote git URL.
func IsLocalPath(resolvedPath string) bool {
	return !isRemoteURI(resolvedPath)
}

// looksLikeLocalPath returns true if the path appears to be an explicit local filesystem reference
// (e.g., ".", "..", starts with ./, ../, or is an absolute path).
func looksLikeLocalPath(path string) bool {
	return path == "." ||
		path == ".." ||
		strings.HasPrefix(path, "./") ||
		strings.HasPrefix(path, "../") ||
		strings.HasPrefix(path, `..\`) ||
		strings.HasPrefix(path, `.\`) ||
		filepath.IsAbs(path)
}

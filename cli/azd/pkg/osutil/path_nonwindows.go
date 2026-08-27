// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package osutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func readContainedFile(rootPath, relativePath string) ([]byte, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return readContainedFileFromRoot(root, relativePath)
}

func readContainedFileFromRoot(root *os.Root, relativePath string) ([]byte, error) {
	data, err := root.ReadFile(relativePath)
	if err == nil {
		return data, err
	}
	if pathContainsUnsafeLink(root, relativePath) {
		return nil, errPathEscapesRoot
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return nil, err
}

func pathContainsUnsafeLink(root *os.Root, relativePath string) bool {
	var prefix string
	for _, component := range strings.Split(relativePath, string(os.PathSeparator)) {
		prefix = filepath.Join(prefix, component)
		info, err := root.Lstat(prefix)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if _, err := root.Stat(prefix); err != nil {
			return true
		}
	}
	return false
}

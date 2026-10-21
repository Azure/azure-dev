// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package localio

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Check rejects symlinks and special files without creating parent directories.
func Check(path string) (bool, error) {
	parent, err := openDirectory(filepath.Dir(path), false)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer parent.Close()
	return checkFile(parent, filepath.Base(path))
}

func checkFile(parent *os.Root, name string) (bool, error) {
	info, err := parent.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("output must be a regular file, not a symlink or special file")
	}
	return true, nil
}

// Write publishes a complete private temporary file in an anchored directory.
// Link provides atomic no-clobber publication; Rename is used only for explicit replacement.
func Write(path string, content []byte, replace bool) (err error) {
	parentPath, name := filepath.Dir(path), filepath.Base(path)
	parent, err := openDirectory(parentPath, true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	identity, err := parent.Stat(".")
	if err != nil {
		return err
	}
	exists, err := checkFile(parent, name)
	if err != nil {
		return err
	}
	if exists && !replace {
		return fs.ErrExist
	}
	temporary := ".cu-" + rand.Text() + ".tmp"
	file, err := parent.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		removeErr := parent.Remove(temporary)
		if !errors.Is(removeErr, fs.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	_, writeErr := file.Write(content)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if err = errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	if err = verifyDirectory(parentPath, identity); err != nil {
		return err
	}
	if _, err = checkFile(parent, name); err != nil {
		return err
	}
	if replace {
		err = parent.Rename(temporary, name)
	} else {
		err = parent.Link(temporary, name)
	}
	if err != nil {
		return err
	}
	return verifyDirectory(parentPath, identity)
}

func verifyDirectory(path string, identity fs.FileInfo) error {
	current, err := openDirectory(path, false)
	if err != nil {
		return err
	}
	defer current.Close()
	info, err := current.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(identity, info) {
		return fmt.Errorf("output directory changed during publication")
	}
	return nil
}

func openDirectory(path string, create bool) (*os.Root, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	volume := filepath.VolumeName(absolute)
	rootPath := volume + string(filepath.Separator)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	relative := strings.TrimPrefix(absolute, rootPath)
	if relative == "" {
		return root, nil
	}
	for part := range strings.SplitSeq(relative, string(filepath.Separator)) {
		info, err := root.Lstat(part)
		if errors.Is(err, fs.ErrNotExist) && create {
			err = root.Mkdir(part, 0o755)
			if err == nil || errors.Is(err, fs.ErrExist) {
				info, err = root.Lstat(part)
			}
		}
		if err != nil {
			_ = root.Close()
			return nil, err
		}
		if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			_ = root.Close()
			return nil, fmt.Errorf("output parent must be a directory without symlinks")
		}
		next, err := root.OpenRoot(part)
		if err != nil {
			_ = root.Close()
			return nil, err
		}
		actual, statErr := next.Stat(".")
		closeErr := root.Close()
		if statErr != nil || closeErr != nil || !os.SameFile(info, actual) {
			_ = next.Close()
			if statErr != nil || closeErr != nil {
				return nil, errors.Join(statErr, closeErr)
			}
			return nil, fmt.Errorf("output directory changed while opening")
		}
		root = next
	}
	return root, nil
}

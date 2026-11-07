// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package localio

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Check rejects symlinks and special files without creating parent directories.
func Check(path string) (exists bool, err error) {
	defer protectError(&err)
	parent, err := openDirectory(filepath.Dir(path), false)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	return checkFile(parent.Root, filepath.Base(path))
}

// MakeExecutable marks a published script executable without following replacement symlinks.
func MakeExecutable(path string) (err error) {
	defer protectError(&err)
	parent, err := openDirectory(filepath.Dir(path), false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	info, err := parent.Lstat(filepath.Base(path))
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("script must be a regular file")
	}
	if err := guardFile(parent.Root, filepath.Base(path)); err != nil {
		return err
	}
	file, err := parent.Open(filepath.Base(path))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	actual, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, actual) {
		return fmt.Errorf("script changed while opening")
	}
	return file.Chmod(info.Mode().Perm() | 0o111)
}

// Read reads one existing regular UTF-8/state file through the same anchored directory rules as Write.
func Read(path string) (body []byte, err error) {
	defer protectError(&err)
	parent, err := openDirectory(filepath.Dir(path), false)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	info, err := parent.Lstat(filepath.Base(path))
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("state must be a regular file")
	}
	if err := guardFile(parent.Root, filepath.Base(path)); err != nil {
		return nil, err
	}
	file, err := parent.Open(filepath.Base(path))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return nil, fmt.Errorf("state changed while opening")
	}
	body, err = io.ReadAll(io.LimitReader(file, 8*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 8*1024*1024 || !utf8.Valid(body) {
		return nil, fmt.Errorf("state must be bounded UTF-8 text")
	}
	return body, nil
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
	return true, guardFile(parent, name)
}

// Write publishes from a private operation-owned staging directory.
// Link provides atomic no-clobber publication; Rename is used only for explicit replacement.
func Write(path string, content []byte, replace bool) (err error) {
	defer protectError(&err)
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
	exists, err := checkFile(parent.Root, name)
	if err != nil {
		return err
	}
	if exists && !replace {
		return fs.ErrExist
	}
	stage, err := newStaging(parent.Root)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, stage.cleanup()) }()
	if _, err = stage.file.Write(content); err != nil {
		return err
	}
	if err = stage.file.Sync(); err != nil {
		return err
	}
	if err = verifyDirectory(parentPath, identity); err != nil {
		return err
	}
	if _, err = checkFile(parent.Root, name); err != nil {
		return err
	}
	if err = stage.publish(name, replace); err != nil {
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

type directory struct {
	*os.Root
	guards []io.Closer
}

func (d *directory) Close() error {
	err := d.Root.Close()
	for _, guard := range d.guards {
		err = errors.Join(err, guard.Close())
	}
	return err
}

func openDirectory(path string, create bool) (_ *directory, err error) {
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
	result := &directory{Root: root}
	defer func() {
		if err != nil {
			err = errors.Join(err, result.Close())
		}
	}()
	relative := strings.TrimPrefix(absolute, rootPath)
	parts := []string{}
	if relative != "" {
		parts = strings.Split(relative, string(filepath.Separator))
	}
	for index := 0; ; index++ {
		guard, guardErr := guardDirectory(result.Root, index == len(parts))
		if guardErr != nil {
			return nil, guardErr
		}
		if guard != nil {
			result.guards = append(result.guards, guard)
		}
		if index == len(parts) {
			return result, nil
		}
		part := parts[index]
		root = result.Root
		info, err := root.Lstat(part)
		if errors.Is(err, fs.ErrNotExist) && create {
			err = root.Mkdir(part, 0o700)
			if err == nil || errors.Is(err, fs.ErrExist) {
				info, err = root.Lstat(part)
			}
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("output parent must be a directory without symlinks")
		}
		next, err := root.OpenRoot(part)
		if err != nil {
			return nil, err
		}
		actual, statErr := next.Stat(".")
		closeErr := root.Close()
		result.Root = next
		if statErr != nil || closeErr != nil || !os.SameFile(info, actual) {
			if statErr != nil || closeErr != nil {
				return nil, errors.Join(statErr, closeErr)
			}
			return nil, fmt.Errorf("output directory changed while opening")
		}
	}
}

type staging struct {
	parent, root *os.Root
	name         string
	identity     fs.FileInfo
	file         *os.File
	payload      fs.FileInfo
}

func newStaging(parent *os.Root) (_ *staging, err error) {
	stage := &staging{parent: parent, name: ".cu-" + rand.Text()}
	if err := parent.Mkdir(stage.name, 0o700); err != nil {
		return nil, err
	}
	stage.identity, err = parent.Lstat(stage.name)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, stage.cleanup())
		}
	}()
	stage.root, err = parent.OpenRoot(stage.name)
	if err != nil {
		return nil, err
	}
	if err = secureStaging(stage.root); err != nil {
		return nil, err
	}
	stage.file, err = stage.root.OpenFile("payload", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	stage.payload, err = stage.file.Stat()
	if err != nil {
		return nil, err
	}
	return stage, nil
}

func (s *staging) verify() error {
	current, err := s.parent.Lstat(s.name)
	if err != nil || !os.SameFile(s.identity, current) {
		return errors.New("staging directory changed")
	}
	if s.file != nil {
		current, err = s.root.Lstat("payload")
		if err != nil || s.payload == nil || !os.SameFile(s.payload, current) {
			return errors.New("staging payload changed")
		}
	}
	return nil
}

func (s *staging) publish(name string, replace bool) error {
	if err := s.verify(); err != nil {
		return err
	}
	source := filepath.Join(s.name, "payload")
	if replace {
		return s.parent.Rename(source, name)
	}
	return s.parent.Link(source, name)
}

func (s *staging) cleanup() (err error) {
	if s.file != nil {
		defer func() { err = errors.Join(err, s.file.Close()) }()
	}
	if s.root != nil {
		defer func() { err = errors.Join(err, s.root.Close()) }()
	}
	current, statErr := s.parent.Lstat(s.name)
	if statErr != nil || !os.SameFile(s.identity, current) {
		return errors.New("staging cleanup refused a changed directory")
	}
	if s.file != nil {
		current, statErr = s.root.Lstat("payload")
		if statErr == nil {
			if s.payload == nil || !os.SameFile(s.payload, current) {
				return errors.New("staging cleanup refused a changed payload")
			}
			if err := s.root.Remove("payload"); err != nil {
				return err
			}
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return statErr
		}
	}
	return s.parent.Remove(s.name)
}

type outputError struct{ cause error }

func (e *outputError) Error() string {
	return "Local output could not be accessed safely; use a private directory owned by the current account."
}

func (e *outputError) Unwrap() error { return e.cause }

func protectError(err *error) {
	if *err != nil {
		*err = &outputError{cause: *err}
	}
}

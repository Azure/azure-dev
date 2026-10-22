// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build linux || darwin

package localio

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"syscall"
)

func guardDirectory(root *os.Root, leaf bool) (io.Closer, error) {
	file, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if err := trustedMode(info, !leaf); err != nil {
		return nil, err
	}
	return nil, platformPermissions(file, false)
}

func trustedMode(info fs.FileInfo, sharedAncestor bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (stat.Uid != 0 && int64(stat.Uid) != int64(os.Geteuid())) {
		return errors.New("output ownership could not be verified")
	}
	// On Linux, the group mode bits include the effective POSIX ACL mask.
	if info.Mode().Perm()&0o022 != 0 && !(sharedAncestor && info.Mode()&fs.ModeSticky != 0) {
		return errors.New("output permissions permit untrusted writers")
	}
	return nil
}

func guardFile(root *os.Root, name string) error {
	file, err := root.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("output must be a regular file")
	}
	if err := trustedMode(info, false); err != nil {
		return err
	}
	return platformPermissions(file, false)
}

func secureStaging(root *os.Root) error {
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if err := trustedMode(info, false); err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("staging directory is not private")
	}
	return platformPermissions(file, true)
}

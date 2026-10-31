// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package spring

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"syscall"
)

func syncCredentialRepairDirectory(root *os.Root, target string) error {
	directory, err := root.Open(path.Dir(target))
	if err != nil {
		return fmt.Errorf("opening credential repair directory: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if errors.Is(syncErr, syscall.EINVAL) || errors.Is(syncErr, syscall.ENOTSUP) {
		syncErr = nil
	}
	if errors.Is(closeErr, fs.ErrClosed) {
		closeErr = nil
	}
	return errors.Join(syncErr, closeErr)
}

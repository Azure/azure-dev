// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package localio

import (
	"errors"
	"math"
	"os"

	"golang.org/x/sys/unix"
)

func platformPermissions(file *os.File, _ bool) error {
	var stat unix.Statfs_t
	fd := file.Fd()
	if fd > math.MaxInt {
		return errors.New("output directory handle is invalid")
	}
	if err := unix.Fstatfs(int(fd), &stat); err != nil {
		return err
	}
	switch stat.Type {
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC,
		unix.TMPFS_MAGIC, unix.OVERLAYFS_SUPER_MAGIC:
		return nil
	default:
		return errors.New("output filesystem permission guarantees are unsupported")
	}
}

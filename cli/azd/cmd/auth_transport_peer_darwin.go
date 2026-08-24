// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build darwin

package cmd

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

func ensureSocketPeerValidationSupported() error {
	return nil
}

func verifySocketPeer(conn *net.UnixConn) error {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return fmt.Errorf("accessing AZD_AUTH_ENDPOINT socket: %w", err)
	}

	var (
		cred    *unix.Xucred
		credErr error
	)
	if err := rawConn.Control(func(fd uintptr) {
		// A descriptor supplied by RawConn.Control is always a small
		// non-negative value, so the conversion cannot overflow.
		//nolint:gosec // G115: file descriptors always fit in an int.
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return fmt.Errorf("accessing AZD_AUTH_ENDPOINT socket handle: %w", err)
	}
	if credErr != nil {
		return fmt.Errorf("reading AZD_AUTH_ENDPOINT socket peer credentials: %w", credErr)
	}
	return verifySocketPeerUID(int64(cred.Uid))
}

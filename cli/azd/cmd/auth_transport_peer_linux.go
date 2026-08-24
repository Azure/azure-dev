// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build linux

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
		cred    *unix.Ucred
		credErr error
	)
	if err := rawConn.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return fmt.Errorf("accessing AZD_AUTH_ENDPOINT socket handle: %w", err)
	}
	if credErr != nil {
		return fmt.Errorf("reading AZD_AUTH_ENDPOINT socket peer credentials: %w", credErr)
	}
	return verifySocketPeerUID(cred.Uid)
}

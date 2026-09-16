// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build unix && !linux && !darwin

package cmd

import (
	"fmt"
	"net"
)

func ensureSocketPeerValidationSupported() error {
	return fmt.Errorf(
		"AZD_AUTH_ENDPOINT scheme 'unix' is not supported on this platform because peer validation is unavailable")
}

func verifySocketPeer(*net.UnixConn) error {
	return ensureSocketPeerValidationSupported()
}

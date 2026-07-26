// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package processutil

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// The Windows graceful stop is deliberately absent rather than merely unimplemented, and
// Terminate depends on it reporting failure so that the control flow escalates to
// forceKill instead of waiting out a grace period that would never end in an exit. If
// someone later "fixes" this to return nil, forced termination silently becomes a no-op
// followed by a timeout, so the contract is asserted rather than assumed.
func TestSignalGraceful_IsUnsupportedOnWindows(t *testing.T) {
	err := signalGraceful(os.Getpid())

	require.Error(t, err, "windows must report that graceful stop is unavailable")
	require.Contains(t, err.Error(), "not supported")
}

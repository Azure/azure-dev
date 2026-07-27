// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package processutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/proctest"
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

// forceKill decides scope against the process object behind its own open handle rather
// than looking the PID up a second time. A process outside the scope is refused at the
// moment of the kill, which is what makes the containment guarantee survive a PID that
// was recycled after Terminate's earlier check.
func TestForceKill_RefusesProcessOutsideScope(t *testing.T) {
	base := t.TempDir()

	extensionDir := filepath.Join(base, "extension")
	require.NoError(t, os.MkdirAll(extensionDir, 0o755))

	elsewhere := filepath.Join(base, "elsewhere")
	require.NoError(t, os.MkdirAll(elsewhere, 0o755))

	helper := proctest.StartIn(t, extensionDir, "azd-forcekill-scope-helper", proctest.ModeIdle)
	defer helper.Stop()

	outOfScope, err := normalizeScope(elsewhere)
	require.NoError(t, err)

	require.ErrorIs(t, forceKill(helper.PID, outOfScope), ErrProcessOutOfScope)
	helper.RequireRunning(t)

	inScope, err := normalizeScope(extensionDir)
	require.NoError(t, err)

	require.NoError(t, forceKill(helper.PID, inScope), "a process inside the scope must still be stopped")
	helper.RequireStopped(t)
}

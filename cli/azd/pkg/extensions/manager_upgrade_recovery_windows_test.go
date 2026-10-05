// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestPrepareUpgradeRecoveryWindowsTransientLock(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)
	extensionDir := filepath.Join(configDir, "extensions", "test.lock")
	require.NoError(t, os.MkdirAll(extensionDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(extensionDir, "installed.exe"), []byte("installed bytes"), 0o600))
	path, err := windows.UTF16PtrFromString(extensionDir)
	require.NoError(t, err)
	handle, err := windows.CreateFile(
		path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0,
	)
	require.NoError(t, err)

	// Verify the real sharing lock before releasing it during the retry backoff.
	probeErr := os.Rename(extensionDir, extensionDir+".probe")
	closed := make(chan error, 1)
	go func() {
		// justified: release an actual Windows filesystem lock during the existing one-second retry.
		time.Sleep(150 * time.Millisecond)
		closed <- windows.CloseHandle(handle)
	}()
	t.Cleanup(func() { require.NoError(t, <-closed) })
	require.Error(t, probeErr)
	require.True(t, errors.Is(probeErr, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(probeErr, windows.ERROR_ACCESS_DENIED))
	manager := &Manager{}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	finish, err := manager.prepareUpgradeRecovery(ctx, &Extension{Id: "test.lock", Version: "1.0.0"})
	require.NoError(t, err)
	backups, err := filepath.Glob(filepath.Join(configDir, "extensions", ".upgrade-backup-*"))
	require.NoError(t, err)
	require.Len(t, backups, 1)
	content, err := os.ReadFile(filepath.Join(backups[0], "installed", "installed.exe"))
	require.NoError(t, err)
	require.Equal(t, "installed bytes", string(content))
	require.NoError(t, finish(t.Context(), false))
}

func TestPrepareUpgradeRecoveryWindowsCancellation(t *testing.T) {
	for _, locked := range []bool{false, true} {
		name := "already canceled"
		if locked {
			name = "deadline during sharing lock"
		}
		t.Run(name, func(t *testing.T) {
			configDir := t.TempDir()
			t.Setenv("AZD_CONFIG_DIR", configDir)
			extensionDir := filepath.Join(configDir, "extensions", "test.cancel")
			require.NoError(t, os.MkdirAll(extensionDir, 0o700))
			installedPath := filepath.Join(extensionDir, "installed.exe")
			require.NoError(t, os.WriteFile(installedPath, []byte("installed bytes"), 0o600))
			ctx, cancel := context.WithCancel(t.Context())
			wantError := error(context.Canceled)
			if locked {
				path, err := windows.UTF16PtrFromString(extensionDir)
				require.NoError(t, err)
				handle, err := windows.CreateFile(
					path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
					nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0,
				)
				require.NoError(t, err)
				defer func() { require.NoError(t, windows.CloseHandle(handle)) }()
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
				wantError = context.DeadlineExceeded
			} else {
				cancel()
			}
			defer cancel()
			installed := &Extension{Id: "test.cancel", Version: "1.0.0", InstalledAsDependency: true}
			before, err := json.Marshal(installed)
			require.NoError(t, err)
			manager := &Manager{}
			finish, err := manager.prepareUpgradeRecovery(ctx, installed)
			require.ErrorIs(t, err, wantError)
			require.Nil(t, finish)
			after, err := json.Marshal(installed)
			require.NoError(t, err)
			require.Equal(t, before, after)
			content, err := os.ReadFile(installedPath)
			require.NoError(t, err)
			require.Equal(t, "installed bytes", string(content))
			backups, err := filepath.Glob(filepath.Join(configDir, "extensions", ".upgrade-backup-*"))
			require.NoError(t, err)
			require.Empty(t, backups)
		})
	}
}

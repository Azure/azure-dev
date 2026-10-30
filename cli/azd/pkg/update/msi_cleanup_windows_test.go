// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package update

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCleanMSIBackup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "azd-update-backup")
	require.NoError(t, os.Mkdir(dir, 0755))
	backup := filepath.Join(dir, "azd.exe")
	require.NoError(t, os.WriteFile(backup, []byte("old executable"), 0600))

	var output bytes.Buffer
	cleanMSIBackup(backup, &output)
	require.NoDirExists(t, dir)
	require.Empty(t, output.String())
}

func TestMSIBackupCleanupRetries(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "O'Connor & Tools")
	require.NoError(t, os.Mkdir(dir, 0755))
	backup := filepath.Join(dir, "azd.exe")
	require.NoError(t, os.WriteFile(backup, []byte("old executable"), 0600))
	locked, err := os.Open(backup)
	require.NoError(t, err)
	t.Cleanup(func() { _ = locked.Close() })
	require.Error(t, os.Remove(backup), "the test must hold a real Windows deletion lock")

	cmd, err := msiBackupCleanupCommand(backup, 5)
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(dir, "cleanup.log"))
		return err == nil
	}, 5*time.Second, 50*time.Millisecond, "the helper must observe the deletion lock before it is released")
	require.NoError(t, locked.Close())
	require.NoError(t, cmd.Wait())
	require.NoDirExists(t, dir)
}

func TestMSIBackupCleanupFailure(t *testing.T) {
	for _, locked := range []bool{false, true} {
		name := "unexpected file"
		if locked {
			name = "persistent sharing lock"
		}
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "azd-update-backup")
			require.NoError(t, os.Mkdir(dir, 0755))
			backup := filepath.Join(dir, "azd.exe")
			require.NoError(t, os.WriteFile(backup, []byte("old executable"), 0600))
			retained := backup
			if locked {
				file, err := os.Open(backup)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, file.Close()) })
			} else {
				retained = filepath.Join(dir, "keep.txt")
				require.NoError(t, os.WriteFile(retained, []byte("unrelated content"), 0600))
			}

			cmd, err := msiBackupCleanupCommand(backup, 2)
			require.NoError(t, err)
			start := time.Now()
			require.Error(t, cmd.Run())
			require.Less(t, time.Since(start), 10*time.Second, "cleanup retries must be bounded")
			require.FileExists(t, retained)
			log, err := os.ReadFile(filepath.Join(dir, "cleanup.log"))
			require.NoError(t, err)
			require.NotEmpty(t, log)
		})
	}
}

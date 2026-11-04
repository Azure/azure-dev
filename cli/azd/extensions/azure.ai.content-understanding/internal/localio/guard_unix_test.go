// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build linux || darwin

package localio

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"azure.ai.content-understanding/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutputRejectsUntrustedPermissionsBeforeCreatingData(t *testing.T) {
	for _, mode := range []fs.FileMode{0o777, 0o770, 0o777 | fs.ModeSticky} {
		t.Run(mode.String(), func(t *testing.T) {
			directory := testutil.TempDir(t)
			output := filepath.Join(directory, "private-result")
			_, err := Check(output)
			require.NoError(t, err)
			require.NoError(t, os.Chmod(directory, mode))
			_, err = Check(output)
			require.Error(t, err)
			require.Error(t, Write(output, []byte("private-content"), false))
			entries, err := os.ReadDir(directory)
			require.NoError(t, err)
			assert.Empty(t, entries, "unsafe leaf must be rejected before staging")
			if mode&fs.ModeSticky == 0 {
				require.Error(t, Write(filepath.Join(directory, "new", "result"), []byte("private-content"), false))
				entries, err = os.ReadDir(directory)
				require.NoError(t, err)
				assert.Empty(t, entries)
			} else {
				require.NoError(t, Write(filepath.Join(directory, "private", "result"), []byte("private-content"), false))
				info, err := os.Stat(filepath.Join(directory, "private"))
				require.NoError(t, err)
				assert.Equal(t, fs.FileMode(0o700), info.Mode().Perm())
			}
			info, err := os.Stat(directory)
			require.NoError(t, err)
			assert.Equal(t, mode.Perm(), info.Mode().Perm(), "never repair user permissions")
		})
	}
}

type foreignOwnerInfo struct{ fs.FileInfo }

func (foreignOwnerInfo) Sys() any { return &syscall.Stat_t{Uid: ^uint32(0)} }

func TestOutputRejectsUntrustedOwnerAndMutableFiles(t *testing.T) {
	directory := testutil.TempDir(t)
	info, err := os.Stat(directory)
	require.NoError(t, err)
	require.NoError(t, trustedMode(info, false))
	require.Error(t, trustedMode(foreignOwnerInfo{info}, false))
	path := filepath.Join(directory, "mutable-state")
	require.NoError(t, os.WriteFile(path, []byte("private-state"), 0o600))
	_, err = Read(path)
	require.NoError(t, err)
	// #nosec G302 -- Deliberately unsafe fixture verifies fail-closed output checks.
	require.NoError(t, os.Chmod(path, 0o666))
	_, err = Check(path)
	require.Error(t, err)
	_, err = Read(path)
	require.Error(t, err)
	require.Error(t, MakeExecutable(path))
	require.Error(t, Write(path, []byte("replacement"), true))
}

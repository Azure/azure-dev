// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package osutil

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestWriteFileAtomicRenameContentionPreservesTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, []byte("old"), PermissionFile))

	pathPtr, err := windows.UTF16PtrFromString(path)
	require.NoError(t, err)
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ,
		0,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	writeErr := WriteFileAtomic(ctx, path, []byte("new"), PermissionFile)
	require.ErrorIs(t, writeErr, context.DeadlineExceeded)
	require.NoError(t, windows.CloseHandle(handle))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "old", string(data))

	tempFiles, err := filepath.Glob(filepath.Join(dir, ".config.json.tmp-*"))
	require.NoError(t, err)
	require.Empty(t, tempFiles)
}

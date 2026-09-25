// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package osutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestReadFileRetriesSharingContention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"value":true}`), PermissionFile))

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

	released := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = windows.CloseHandle(handle)
		close(released)
	}()

	data, readErr := ReadFile(path)
	<-released

	require.NoError(t, readErr)
	require.JSONEq(t, `{"value":true}`, string(data))
}

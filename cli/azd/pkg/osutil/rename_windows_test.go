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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestRename(t *testing.T) {
	t.Run("Source In Use", func(t *testing.T) {

		dir := t.TempDir()
		file, err := os.Create(filepath.Join(dir, "old"))
		require.NoError(t, err)

		// Wait for a moment before closing the file. This allows Rename to exercise the retry logic for a sharing violation
		// since while we hold the file open, os.Rename will fail.
		// justified: platform-specific Windows filesystem behaviour — the sleep holds the
		// file handle open long enough for os.Rename's retry loop to observe a sharing
		// violation before the handle is released.
		go func() {
			time.Sleep(1 * time.Second)
			file.Close()
		}()

		err = Rename(t.Context(), filepath.Join(dir, "old"), filepath.Join(dir, "new"))
		assert.NoError(t, err)
	})

	t.Run("Destination In Use", func(t *testing.T) {
		dir := t.TempDir()
		file, err := os.Create(filepath.Join(dir, "old"))
		require.NoError(t, err)
		err = file.Close()
		require.NoError(t, err)

		file, err = os.Create(filepath.Join(dir, "new"))
		require.NoError(t, err)

		// Wait for a moment before closing the target. This allows Rename to exercise the retry logic for an access is
		// denied error since we hold the file open, os.Rename will fail.
		// justified: platform-specific Windows filesystem behaviour — the sleep holds the
		// destination file handle open long enough for os.Rename's retry loop to observe
		// the access-denied error before the handle is released.

		go func() {
			time.Sleep(1 * time.Second)
			file.Close()
		}()

		err = Rename(t.Context(), filepath.Join(dir, "old"), filepath.Join(dir, "new"))

		assert.NoError(t, err)
	})
}

func TestRemoveAll_FileInUse(t *testing.T) {
	dir := t.TempDir()
	file, err := os.Create(filepath.Join(dir, "locked"))
	require.NoError(t, err)

	go func() {
		time.Sleep(time.Second)
		_ = file.Close()
	}()

	assert.NoError(t, RemoveAll(t.Context(), dir))
	assert.NoDirExists(t, dir)
}

func TestRetryFileSystemOperation_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	attempts := 0

	err := retryFileSystemOperation(ctx, "test operation", func() error {
		attempts++
		cancel()
		return windows.ERROR_SHARING_VIOLATION
	})

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, attempts)
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows
// +build windows

package osutil

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRename(t *testing.T) {
	t.Run("Source In Use", func(t *testing.T) {

		dir := t.TempDir()
		file, err := os.Create(filepath.Join(dir, "old"))
		assert.NoError(t, err)

		// Wait for a moment before closing the file. This allows Rename to exercise the retry logic for a sharing violation
		// since while we hold the file open, os.Rename will fail.
		go func() {
			time.Sleep(1 * time.Second)
			file.Close()
		}()

		err = Rename(context.Background(), filepath.Join(dir, "old"), filepath.Join(dir, "new"))
		assert.NoError(t, err)
	})

	t.Run("Destination In Use", func(t *testing.T) {
		dir := t.TempDir()
		file, err := os.Create(filepath.Join(dir, "old"))
		assert.NoError(t, err)
		err = file.Close()
		assert.NoError(t, err)

		file, err = os.Create(filepath.Join(dir, "new"))
		assert.NoError(t, err)

		// Wait for a moment before closing the target. This allows Rename to exercise the retry logic for an access is
		// denied error since we hold the file open, os.Rename will fail.

		go func() {
			time.Sleep(1 * time.Second)
			file.Close()
		}()

		err = Rename(context.Background(), filepath.Join(dir, "old"), filepath.Join(dir, "new"))

		assert.NoError(t, err)
	})
}

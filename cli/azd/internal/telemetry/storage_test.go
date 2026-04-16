// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this file intentionally interacts with the filesystem (important implementation detail).
// As such, it might be susceptible to filesystem related failures and also general slowness.

const fileExtension = ".itm"

var itemKeptTime = time.Duration(24) * time.Hour

func TestNewStorageQueue(t *testing.T) {
	t.Parallel()
	folder := t.TempDir()

	t.Run("CreatesFolder", func(t *testing.T) {
		err := os.RemoveAll(folder)
		assert.NoError(t, err)

		storage, err := NewStorageQueue(folder, fileExtension, itemKeptTime)
		assert.NoError(t, err)
		assert.DirExists(t, storage.folder)
	})

	t.Run("HandlesExistingFolder", func(t *testing.T) {
		err := os.MkdirAll(folder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		storage, err := NewStorageQueue(folder, fileExtension, itemKeptTime)
		assert.NoError(t, err)
		assert.DirExists(t, storage.folder)
	})
}

func TestFifoQueue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	messages := []string{
		"Message1",
		"Message2",
		"Message3",
	}

	mockClock := clock.NewMock()
	storage := setupStorageQueue(t, dir)
	storage.clock = mockClock

	// Queue items with distinct timestamps by advancing the mock clock.
	// This ensures unique timestamp prefixes in filenames. After enqueuing,
	// we set explicit modification times to guarantee deterministic FIFO
	// ordering in Peek(), which orders by mtime.
	enqueueAndAssert(storage, messages[0], t)
	mockClock.Add(time.Second)

	enqueueAndAssert(storage, messages[1], t)
	mockClock.Add(time.Second)

	enqueueAndAssert(storage, messages[2], t)

	// Set monotonically increasing mtimes on item files to ensure
	// deterministic ordering. Some file systems have coarse mtime
	// granularity, so rapid sequential writes may share the same mtime.
	ensureFifoModTimes(t, dir)

	// Pop all items sequentially
	item, err := storage.Peek()
	assert.NoError(t, err)
	assert.Equal(t, messages[0], string(item.Message()))
	err = storage.Remove(item)
	assert.NoError(t, err)

	item, err = storage.Peek()
	assert.NoError(t, err)
	assert.Equal(t, messages[1], string(item.Message()))
	err = storage.Remove(item)
	assert.NoError(t, err)

	item, err = storage.Peek()
	assert.NoError(t, err)
	assert.Equal(t, messages[2], string(item.Message()))
	err = storage.Remove(item)
	assert.NoError(t, err)

	// Assert nothing remains
	itm, err := storage.Peek()
	assert.NoError(t, err)
	assert.Nil(t, itm)
}

func TestEnqueueWithDelay(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mockClock := clock.NewMock()
	now := mockClock.Now()
	enqueueTimeDelay := time.Duration(1) * time.Hour

	storage := setupStorageQueue(t, dir)
	storage.clock = mockClock

	message := "any"
	retryCount := 2
	err := storage.EnqueueWithDelay([]byte(message), enqueueTimeDelay, retryCount)
	assert.NoError(t, err)

	item, err := storage.Peek()
	assert.NoError(t, err)
	assert.Nil(t, item, "")

	// Advance the clock. Item should now be visible.
	mockClock.Set(now.Add(enqueueTimeDelay))
	item, err = storage.Peek()
	assert.NoError(t, err)
	assert.NotNil(t, item)
	assert.Equal(t, message, string(item.Message()))
	assert.Equal(t, retryCount, item.RetryCount())

	// Advance the clock past max time kept. Item should be invisible.
	mockClock.Set(now.Add(enqueueTimeDelay + storage.itemFileMaxTimeKept))
	item, err = storage.Peek()
	assert.NoError(t, err)
	assert.Nil(t, item, "")
}

func TestEnqueueWithDelay_ZeroDelay(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mockClock := clock.NewMock()

	storage := setupStorageQueue(t, dir)
	storage.clock = mockClock

	message := "any"
	retryCount := 1
	err := storage.EnqueueWithDelay([]byte(message), time.Duration(0), retryCount)
	assert.NoError(t, err)

	item, err := storage.Peek()
	assert.NoError(t, err)
	assert.NotNil(t, item)
	assert.Equal(t, message, string(item.Message()))
	assert.Equal(t, retryCount, item.RetryCount())
}

func enqueueAndAssert(storage *StorageQueue, message string, t *testing.T) {
	err := storage.Enqueue([]byte(message))
	assert.NoError(t, err)
}

func TestPeekWhenNoItemsExist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	storage := setupStorageQueue(t, dir)

	itm, err := storage.Peek()
	assert.NoError(t, err)
	assert.Nil(t, itm)
}

func TestRemoveInvalidItem(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	storage := setupStorageQueue(t, dir)

	err := storage.Remove(&StoredItem{
		retryCount: 0,
		message:    []byte{},
		fileName:   "DoesNotExist",
	})
	assert.NoError(t, err)
}

func setupStorageQueue(t *testing.T, tempDir string) *StorageQueue {
	storage, err := NewStorageQueue(tempDir, fileExtension, itemKeptTime)
	assert.NoError(t, err)
	return storage
}

// ensureFifoModTimes sets monotonically increasing modification times on
// item files in the directory, sorted by filename. This guarantees
// deterministic FIFO ordering in Peek(), which uses file modification time.
func ensureFifoModTimes(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	// Filter to item files and sort by name (which includes the timestamp)
	var itemFiles []os.DirEntry
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), fileExtension) {
			itemFiles = append(itemFiles, e)
		}
	}
	slices.SortFunc(itemFiles, func(a, b os.DirEntry) int {
		return cmp.Compare(a.Name(), b.Name())
	})

	// Set distinct, monotonically increasing mtimes
	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, f := range itemFiles {
		mtime := baseTime.Add(time.Duration(i) * time.Second)
		path := filepath.Join(dir, f.Name())
		require.NoError(t, os.Chtimes(path, mtime, mtime))
	}
}

func TestStorageQueue_Cleanup(t *testing.T) {
	t.Parallel()
	mockClock := clock.NewMock()
	now := mockClock.Now()
	nowTimeStr := now.UTC().Format(fsTimeLayout)
	almostStaleTime := now.Add(itemKeptTime - time.Duration(1)*time.Second)
	staleTime := now.Add(itemKeptTime)

	tests := []struct {
		name string
		// Either set items or files, not both.
		filesPresent []string
		itemsPresent []string
		createTime   time.Time
		cleanupTime  time.Time
		// Either set items or files, not both.
		expectedFilesRemaining []string
		expectedItemsRemaining []string
	}{
		{
			name: "ValidTempFiles",
			filesPresent: []string{
				"1.tmp",
				"2.tmp",
				"3.tmp",
			},
			expectedFilesRemaining: []string{
				"1.tmp",
				"2.tmp",
				"3.tmp",
			},
		},
		{
			name: "AlmostStaleTempFiles",
			filesPresent: []string{
				"AlmostStale1.tmp",
				"AlmostStale2.tmp",
				"AlmostStale3.tmp",
			},
			// We can't mock file creation time on the filesystem, hence, we:
			// 1. Set createTime to match current time
			// 2. Set cleanupTime to be in the future.
			createTime:  time.Now(),
			cleanupTime: time.Now().Add(tempFileTtl - time.Duration(2)*time.Second),
			expectedFilesRemaining: []string{
				"AlmostStale1.tmp",
				"AlmostStale2.tmp",
				"AlmostStale3.tmp",
			},
		},
		{
			name: "StaleTempFiles",
			filesPresent: []string{
				"stale1.tmp",
				"stale2.tmp",
				"stale3.tmp",
			},
			// We can't mock file creation time on the filesystem, hence, we:
			// 1. Set createTime to match current time
			// 2. Set cleanupTime to be in the future.
			createTime:             time.Now(),
			cleanupTime:            time.Now().Add(tempFileTtl + time.Duration(1)*time.Minute),
			expectedFilesRemaining: []string{},
		},
		{
			name: "ValidItemFiles",
			filesPresent: []string{
				nowTimeStr + "_1_100" + fileExtension,
				nowTimeStr + "_1_101" + fileExtension,
				nowTimeStr + "_1_102" + fileExtension,
			},
			expectedFilesRemaining: []string{
				nowTimeStr + "_1_100" + fileExtension,
				nowTimeStr + "_1_101" + fileExtension,
				nowTimeStr + "_1_102" + fileExtension,
			},
		},
		{
			name: "InvalidItemFiles",
			filesPresent: []string{
				"InvalidFormat" + fileExtension,
				"NotADate_1" + fileExtension,
				fsTimeLayout + "_NotANumber" + fileExtension,
			},
			expectedFilesRemaining: []string{},
		},
		{
			name: "AlmostStaleItemFiles",
			filesPresent: []string{
				nowTimeStr + "_1_100" + fileExtension,
				nowTimeStr + "_1_101" + fileExtension,
				nowTimeStr + "_1_102" + fileExtension,
			},
			expectedFilesRemaining: []string{
				nowTimeStr + "_1_100" + fileExtension,
				nowTimeStr + "_1_101" + fileExtension,
				nowTimeStr + "_1_102" + fileExtension,
			},
			cleanupTime: almostStaleTime,
		},
		{
			name: "StaleItemFiles",
			filesPresent: []string{
				nowTimeStr + "_1_100" + fileExtension,
				nowTimeStr + "_1_101" + fileExtension,
				nowTimeStr + "_1_102" + fileExtension,
			},
			cleanupTime:            staleTime,
			expectedFilesRemaining: []string{},
		},
		{
			name:                   "EnqueuedItems",
			itemsPresent:           []string{"a", "b", "c"},
			expectedItemsRemaining: []string{"a", "b", "c"},
		},
		{
			name:                   "EnqueuedItemsAlmostStale",
			itemsPresent:           []string{"a", "b", "c"},
			expectedItemsRemaining: []string{"a", "b", "c"},
			cleanupTime:            almostStaleTime,
		},
		{
			name:                   "EnqueuedItemsStale",
			itemsPresent:           []string{"a", "b", "c"},
			expectedItemsRemaining: []string{},
			cleanupTime:            staleTime,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClock.Set(now)
			dir := t.TempDir()
			storage := setupStorageQueue(t, dir)
			storage.clock = mockClock

			if !tt.createTime.IsZero() {
				mockClock.Set(tt.createTime)
			}

			// Create files directly on filesystem
			for _, file := range tt.filesPresent {
				f, err := os.Create(filepath.Join(dir, file))
				require.NoError(t, err)

				f.Close()
			}

			// Create items
			for _, item := range tt.itemsPresent {
				enqueueAndAssert(storage, item, t)
			}

			if !tt.cleanupTime.IsZero() {
				mockClock.Set(tt.cleanupTime)
			}
			storage.Cleanup(context.Background(), make(chan struct{}, 1))

			remainingFiles, err := os.ReadDir(storage.folder)
			assert.NoError(t, err)
			assert.Len(t, remainingFiles, len(tt.expectedFilesRemaining)+len(tt.expectedItemsRemaining))
			for _, remainingFile := range remainingFiles {
				// Validate for a known filename
				if slices.Contains(tt.expectedFilesRemaining, remainingFile.Name()) {
					// Validate for a known file item
					content, err := os.ReadFile(filepath.Join(storage.folder, remainingFile.Name()))
					assert.NoError(t, err)

					if slices.Contains(tt.expectedItemsRemaining, string(content)) {
						assert.Fail(
							t,
							fmt.Sprintf(
								"Unknown remaining file found. Filename: %s, content: %s. Expected filenames: %v, "+
									"expected content: %v. ",
								remainingFile.Name(),
								string(content),
								tt.expectedFilesRemaining,
								tt.expectedItemsRemaining,
							),
						)
					}
				}
			}
		})
	}
}

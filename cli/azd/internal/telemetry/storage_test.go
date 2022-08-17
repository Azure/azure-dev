package telemetry

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/assert"
	"golang.org/x/exp/maps"
)

// The tests in this file intentionally interacts with the filesystem (important implementation detail).
// As such, it might be susceptible to filesystem related failures and also general slowness.

const fileExtension = ".itm"

func TestNewStorageQueue(t *testing.T) {
	folder := t.TempDir()

	t.Run("CreatesFolder", func(t *testing.T) {
		err := os.RemoveAll(folder)
		assert.NoError(t, err)

		storage, err := NewStorageQueue(folder, fileExtension)
		assert.NoError(t, err)
		assert.DirExists(t, storage.folder)
	})

	t.Run("HandlesExistingFolder", func(t *testing.T) {
		err := os.MkdirAll(folder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		storage, err := NewStorageQueue(folder, fileExtension)
		assert.NoError(t, err)
		assert.DirExists(t, storage.folder)
	})
}

func TestFifoQueue(t *testing.T) {
	dir := t.TempDir()
	messages := []string{
		"Message1",
		"Message2",
		"Message3",
	}

	storage := setupStorageQueue(t, dir)

	// Queue 3 items
	// Milliseconds of sleep is added between each queue attempt to ensure that no item shares the same
	// file modification time (which is used for ordering) on certain file systems that have granularity of milliseconds.
	// This is only for determinism in assertions. In practice, the ordering of two messages delivered around the same millisecond intervals
	// is not important.
	enqueueAndAssert(storage, messages[0], t)
	time.Sleep(time.Millisecond * 10)

	enqueueAndAssert(storage, messages[1], t)
	time.Sleep(time.Millisecond * 10)

	enqueueAndAssert(storage, messages[2], t)

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
	dir := t.TempDir()
	mockClock := clock.NewMock()

	storage := setupStorageQueue(t, dir)
	storage.clock = mockClock

	message := "any"
	retryCount := 2
	err := storage.EnqueueWithDelay([]byte(message), time.Duration(1)*time.Hour, retryCount)
	assert.NoError(t, err)

	item, err := storage.Peek()
	assert.NoError(t, err)
	assert.Nil(t, item, "")

	// Advance the clock. Item should now be visible.
	mockClock.Add(time.Duration(2) * time.Hour)
	item, err = storage.Peek()
	assert.NoError(t, err)
	assert.NotNil(t, item)
	assert.Equal(t, message, string(item.Message()))
	assert.Equal(t, retryCount, item.RetryCount())
}

func TestEnqueueWithDelay_ZeroDelay(t *testing.T) {
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
	dir := t.TempDir()
	storage := setupStorageQueue(t, dir)

	itm, err := storage.Peek()
	assert.NoError(t, err)
	assert.Nil(t, itm)
}

func TestRemoveInvalidItem(t *testing.T) {
	dir := t.TempDir()
	storage := setupStorageQueue(t, dir)

	err := storage.Remove(&StoredItem{
		retryCount: 0,
		message:    []byte{},
		fileName:   "DoesNotExist",
	})
	assert.NoError(t, err)
}

func TestCleanup(t *testing.T) {
	dir := t.TempDir()

	invalidFiles := []string{
		"InvalidFormat" + fileExtension,
		"NotADate_1" + fileExtension,
		fsTimeLayout + "_NotANumber" + fileExtension,
	}

	staleFiles := []string{
		"stale1.tmp",
		"stale2.tmp",
		"stale3.tmp",
	}

	validFilesByName := map[string]struct{}{
		fsTimeLayout + "_1_100" + fileExtension: {},
		fsTimeLayout + "_1_101" + fileExtension: {},
		fsTimeLayout + "_1_102" + fileExtension: {},
	}
	validFileNames := maps.Keys(validFilesByName)

	filesToCreate := append(invalidFiles, staleFiles...)
	filesToCreate = append(filesToCreate, validFileNames...)

	for _, file := range filesToCreate {
		f, err := os.Create(filepath.Join(dir, file))
		assert.NoError(t, err)

		f.Close()
	}

	mockClock := clock.NewMock()
	// Set current time to be greater than TTL for stale files to be deleted
	mockClock.Set(time.Now().Add(tempFileTtl + time.Duration(5)*time.Hour))

	storage := setupStorageQueue(t, dir)
	storage.clock = mockClock

	validFilesByContent := map[string]struct{}{
		"item1": {},
		"item2": {},
	}
	validContent := maps.Keys(validFilesByContent)
	enqueueAndAssert(storage, validContent[0], t)
	enqueueAndAssert(storage, validContent[1], t)

	storage.Cleanup()

	remainingFiles, err := os.ReadDir(storage.folder)
	assert.NoError(t, err)
	assert.Len(t, remainingFiles, len(validFilesByName)+len(validFilesByContent))
	for _, remainingFile := range remainingFiles {
		// Validate for a known filename
		if _, ok := validFilesByName[remainingFile.Name()]; !ok {
			// Validate for a known file item
			content, err := os.ReadFile(filepath.Join(storage.folder, remainingFile.Name()))
			assert.NoError(t, err)

			if _, ok := validFilesByContent[string(content)]; !ok {
				assert.Fail(t, fmt.Sprintf("Unknown remaining file found. Filename: %s, content: %s. Expected filenames: %v, expected content: %v. ", remainingFile.Name(), string(content), validFileNames, validContent))
			}
		}
	}
}

func setupStorageQueue(t *testing.T, tempDir string) *StorageQueue {
	storage, err := NewStorageQueue(tempDir, fileExtension)
	assert.NoError(t, err)
	return storage
}

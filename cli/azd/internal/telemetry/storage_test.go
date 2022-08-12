package telemetry

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/assert"
)

// The tests in this file intentionally interacts with the filesystem (important implementation detail).
// As such, it might be susceptible to filesystem related failures and also general slowness.

const fileExtension = ".itm"

type tempFolder struct {
	name string
}

func newTempDir(t *testing.T, name string) tempFolder {
	dirname, err := os.MkdirTemp("", name)
	assert.NoError(t, err)
	return tempFolder{dirname}
}

func (t *tempFolder) close() {
	_ = os.RemoveAll(t.name)
}

func TestNewStorageQueue(t *testing.T) {
	folder := filepath.Join(os.TempDir(), "azdnewstg")
	defer os.RemoveAll(folder)

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
	dir := newTempDir(t, "azdq")
	defer dir.close()

	messages := []string{
		"Message1",
		"Message2",
		"Message3",
	}

	storage := setupStorageQueue(t, dir)
	// Queue 3 items, asserting each one is stored
	item1 := enqueueAndAssert(storage, messages[0], t)
	item2 := enqueueAndAssert(storage, messages[1], t)
	item3 := enqueueAndAssert(storage, messages[2], t)

	// Remove a non-head entry. This is only possible
	// since we Peek the head as we queue each item.
	err := storage.Remove(item1)
	assert.NoError(t, err)

	// Assert head unchanged
	item, err := storage.Peek()
	assert.NoError(t, err)
	assert.Equal(t, messages[2], string(item.Message()))

	// Remove the head
	err = storage.Remove(item3)
	assert.NoError(t, err)

	// Assert head moved
	item, err = storage.Peek()
	assert.NoError(t, err)
	assert.Equal(t, messages[1], string(item.Message()))

	// Remove remaining
	err = storage.Remove(item2)
	assert.NoError(t, err)

	// Assert nothing remains
	itm, err := storage.Peek()
	assert.NoError(t, err)
	assert.Nil(t, itm)
}

func TestEnqueueWithDelay(t *testing.T) {
	dir := newTempDir(t, "azdqd")
	defer dir.close()
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
	dir := newTempDir(t, "azdqdz")
	defer dir.close()
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

func enqueueAndAssert(storage *StorageQueue, message string, t *testing.T) *StoredItem {
	err := storage.Enqueue([]byte(message))
	assert.NoError(t, err)

	item, err := storage.Peek()
	assert.NoError(t, err)
	assert.NotNil(t, item)
	assert.Equal(t, message, string(item.Message()), "Message '%s' was queued, but '%s' was returned after peeking.", message, string(item.Message()))

	return item
}

func TestPeekWhenNoItemsExist(t *testing.T) {
	dir := newTempDir(t, "azdpk")
	defer dir.close()

	storage := setupStorageQueue(t, dir)

	itm, err := storage.Peek()
	assert.NoError(t, err)
	assert.Nil(t, itm)
}

func TestRemoveInvalidItem(t *testing.T) {
	dir := newTempDir(t, "azdriv")
	defer dir.close()

	storage := setupStorageQueue(t, dir)

	err := storage.Remove(&StoredItem{
		retryCount: 0,
		message:    []byte{},
		fileName:   "doesnotexist",
	})
	assert.NoError(t, err)
}

func TestCleanup(t *testing.T) {
	dir := newTempDir(t, "azdcln")
	defer dir.close()

	invalidFiles := []string{
		"invalidformat" + fileExtension,
		"notadate_1" + fileExtension,
		fsTimeLayout + "_notanumber" + fileExtension,
	}

	staleFiles := []string{
		"stale1.tmp",
		"stale2.tmp",
		"stale3.tmp",
	}

	validFiles := []string{
		fsTimeLayout + "_1_100" + fileExtension,
		fsTimeLayout + "_1_101" + fileExtension,
		fsTimeLayout + "_1_102" + fileExtension,
	}

	filesToCreate := append(invalidFiles, staleFiles...)
	filesToCreate = append(filesToCreate, validFiles...)

	for _, file := range filesToCreate {
		f, err := os.Create(filepath.Join(dir.name, file))
		assert.NoError(t, err)

		f.Close()
	}

	mockClock := clock.NewMock()
	// Set current time to be greater than TTL for stale files to be deleted
	mockClock.Set(time.Now().Add(tempFileTtl + time.Duration(5)*time.Hour))

	storage := setupStorageQueue(t, dir)
	storage.clock = mockClock

	item1 := enqueueAndAssert(storage, "item1", t)
	validFiles = append(validFiles, filepath.Base(item1.fileName))

	item2 := enqueueAndAssert(storage, "item2", t)
	validFiles = append(validFiles, filepath.Base(item2.fileName))

	storage.Cleanup()

	remainingFiles, err := os.ReadDir(storage.folder)
	assert.NoError(t, err)
	assert.Len(t, remainingFiles, len(validFiles))
	for _, remainingFile := range remainingFiles {
		assert.Contains(t, validFiles, remainingFile.Name())
	}
}

func setupStorageQueue(t *testing.T, tempDir tempFolder) *StorageQueue {
	storage, err := NewStorageQueue(tempDir.name, fileExtension)
	assert.NoError(t, err)
	return storage
}

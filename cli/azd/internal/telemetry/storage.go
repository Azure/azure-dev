package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/benbjohnson/clock"
)

// A time.Format layout suitable for use in file names.
const fsTimeLayout = "20060102T150405"

// Max time before temp files are cleaned up
const tempFileTtl = time.Duration(5) * time.Minute

type Queue interface {
	Enqueue(message []byte) error
	EnqueueWithDelay(message []byte, delayDuration time.Duration, retryCount int) error
	Peek() (*StoredItem, error)
	Remove(item *StoredItem) error
}

// StorageQueue is a FIFO-based queue backed by disk storage, with items stored as individual files.
// The current implementation allows for multiple producers, single consumer.
//
// Items can be queued by Enqueue or EnqueueWithDelay.
// QueueWithDelay allows for producers to queue items that should not be picked up
// by consumers until after the specified duration has passed. This is useful for retry delay scheduling.
//
// Items can be read by Peek, which will read the next available item.
// Once the item is processed, consumers are responsible for calling Remove to remove the item from the queue.
type StorageQueue struct {
	folder              string
	itemFileExtension   string
	itemFileMaxTimeKept time.Duration

	// Standard time library clock, unless mocked in tests
	clock clock.Clock
}

type StoredItem struct {
	// Number of retries attempted
	retryCount int

	// Message in the item
	message []byte

	// File name of the stored item
	fileName string
}

func (itm *StoredItem) RetryCount() int {
	return itm.retryCount
}

func (itm *StoredItem) Message() []byte {
	return itm.message
}

type itemEntry struct {
	name        string
	readyTime   time.Time
	fileModTime time.Time
	retryCount  int
}

// Creates the storage-based queue.
func NewStorageQueue(
	folder string, itemFileExtension string, itemFileMaxTimeKept time.Duration) (*StorageQueue, error) {
	if err := os.MkdirAll(folder, osutil.PermissionDirectory); err != nil {
		return nil, fmt.Errorf("failed to create storage queue folder: %w", err)
	}

	if !strings.HasPrefix(itemFileExtension, ".") {
		itemFileExtension = "." + itemFileExtension
	}

	storage := StorageQueue{
		folder:              folder,
		itemFileExtension:   itemFileExtension,
		itemFileMaxTimeKept: itemFileMaxTimeKept,
		clock:               clock.New(),
	}
	return &storage, nil
}

// Queues a message.
func (stg *StorageQueue) Enqueue(message []byte) error {
	return stg.save(time.Duration(0), 0, message)
}

// Queues a message with delay.
func (stg *StorageQueue) EnqueueWithDelay(message []byte, delayDuration time.Duration, retryCount int) error {
	return stg.save(delayDuration, retryCount, message)
}

func (stg *StorageQueue) save(delayDuration time.Duration, retryCount int, message []byte) error {
	file, err := os.CreateTemp(stg.folder, "*_itm.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	tempFileName := file.Name()
	err = os.WriteFile(tempFileName, message, osutil.PermissionFile)
	if err != nil {
		_ = removeIfExists(tempFileName)
		return fmt.Errorf("failed to write file: %w", err)
	}
	file.Close()

	generatedFileName := filepath.Base(tempFileName)
	randomSuffix := generatedFileName[:strings.LastIndex(generatedFileName, "_")]
	readyTime := stg.clock.Now().Add(delayDuration)
	fileName := formatFileName(readyTime, retryCount, randomSuffix, stg.itemFileExtension)
	err = os.Rename(tempFileName, filepath.Join(stg.folder, fileName))
	if err != nil {
		_ = removeIfExists(tempFileName)
		return fmt.Errorf("failed to rename file: %w", err)
	}

	return nil
}

// Gets the next available item for processing.
// Returns nil if no items exist.
// Returns error if an error occurs while reading storage.
func (stg *StorageQueue) Peek() (*StoredItem, error) {
	items, err := stg.getAllItemsUnordered()
	if err != nil {
		return nil, fmt.Errorf("failed to get stored files: %w", err)
	}

	leastRecentTime := time.Time{}
	latestIndex := -1
	now := stg.clock.Now()
	for i, item := range items {
		timeSinceReady := now.Sub(item.readyTime)
		if timeSinceReady >= 0 && timeSinceReady < stg.itemFileMaxTimeKept {
			if latestIndex == -1 || item.fileModTime.Before(leastRecentTime) {
				leastRecentTime = item.fileModTime
				latestIndex = i
			}
		}
	}

	if latestIndex == -1 {
		return nil, nil
	}

	item := items[latestIndex]
	fileName := filepath.Join(stg.folder, item.name)
	message, err := os.ReadFile(fileName)
	if err != nil {
		return nil, fmt.Errorf("failed to read latest stored item: %w", err)
	}

	return &StoredItem{
		fileName:   fileName,
		retryCount: item.retryCount,
		message:    message,
	}, nil
}

// Removes the stored item from queue.
// Does not return an error if the item is already removed.
func (stg *StorageQueue) Remove(item *StoredItem) error {
	err := os.Remove(item.fileName)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to remove stored item: %w", err)
	}

	return nil
}

func removeIfExists(filename string) error {
	err := os.Remove(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	return err
}

// Scans the storage directory for any obsoleted items or temp files.
func (stg *StorageQueue) Cleanup(ctx context.Context, done chan (struct{})) {
	defer func() { done <- struct{}{} }()
	files, err := os.ReadDir(stg.folder)
	if err != nil {
		return
	}

	for _, file := range files {
		select {
		case <-ctx.Done():
			return
		default:
			if file.IsDir() {
				continue
			}

			stg.checkFileForCleanup(file)
		}
	}
}

func (stg *StorageQueue) checkFileForCleanup(file fs.DirEntry) {
	name := file.Name()
	if strings.HasSuffix(name, ".tmp") {
		stg.checkTempFileForCleanup(file)
	} else if strings.HasSuffix(name, stg.itemFileExtension) {
		stg.checkItemFileForCleanup(file)
	}
}

func (stg *StorageQueue) checkTempFileForCleanup(file fs.DirEntry) {
	info, err := file.Info()
	if err != nil {
		log.Printf("failed to retrieve old tmp file info for %s: %s", file.Name(), err)
		return
	}

	if stg.clock.Since(info.ModTime()) >= tempFileTtl {
		stg.cleanupItem(file, "old tmp")
	}
}

func (stg *StorageQueue) checkItemFileForCleanup(file fs.DirEntry) {
	item, ok := parseFileName(file.Name())

	if !ok {
		stg.cleanupItem(file, "unparsable item")
		return
	}

	if stg.clock.Since(item.readyTime) >= stg.itemFileMaxTimeKept {
		stg.cleanupItem(file, "old item")
	}
}

func (stg *StorageQueue) cleanupItem(file fs.DirEntry, itemType string) {
	err := removeIfExists(filepath.Join(stg.folder, file.Name()))

	if err != nil {
		log.Printf("failed to remove %s file: %s", itemType, err)
	}
}

func (stg *StorageQueue) getAllItemsUnordered() ([]itemEntry, error) {
	dirEntries, err := readDirUnordered(stg.folder)
	if err != nil {
		return nil, fmt.Errorf("error reading folder: %w", err)
	}

	items := []itemEntry{}

	for _, entry := range dirEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), stg.itemFileExtension) {
			item, ok := readItemEntry(entry)

			if ok {
				items = append(items, *item)
			}
		}
	}

	return items, nil
}

// readDirUnordered is os.ReadDir except it returns entries unordered instead of by name order
func readDirUnordered(name string) ([]os.DirEntry, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	dirs, err := f.ReadDir(-1)
	return dirs, err
}

func formatFileName(readyTime time.Time, retryCount int, uniqueSuffix string, extension string) string {
	return fmt.Sprintf("%s_%d_%s%s",
		readyTime.UTC().Format(fsTimeLayout),
		retryCount,
		uniqueSuffix,
		extension)
}

func readItemEntry(dirEntry os.DirEntry) (*itemEntry, bool) {
	item, ok := parseFileName(dirEntry.Name())
	if !ok {
		return nil, false
	}

	info, err := dirEntry.Info()
	if err != nil {
		return nil, false
	}

	item.fileModTime = info.ModTime()
	return item, true
}

func parseFileName(name string) (*itemEntry, bool) {
	sections := strings.Split(name, "_")
	if len(sections) < 2 {
		return nil, false
	}

	timestamp, err := time.Parse(fsTimeLayout, sections[0])
	if err != nil {
		return nil, false
	}

	retryCount, err := strconv.Atoi(sections[1])
	if err != nil {
		return nil, false
	}

	return &itemEntry{
		name:       name,
		readyTime:  timestamp,
		retryCount: retryCount,
	}, true
}

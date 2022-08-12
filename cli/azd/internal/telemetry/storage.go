package telemetry

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const logDirectoryPermissions = 0755
const transmissionFileExtension = ".azdtel"
const transmissionFileFormat = "20060102T150405"

type Storage struct {
	folder string
}

type StoredTransmission struct {
	// Number of retries attempted
	retryCount int

	// Payload of the transmission
	payload string

	// File name of the stored transmission
	fileName string
}

type transmissionFileEntry struct {
	name       string
	timestamp  time.Time
	retryCount int
}

func NewStorage(folder string) (*Storage, error) {
	if err := os.MkdirAll(folder, logDirectoryPermissions); err != nil {
		return nil, fmt.Errorf("failed to create telemetry storage folder: %v", err)
	}

	storage := Storage{
		folder: folder,
	}
	return &storage, nil
}

// Scans the storage directory for any obsoleted transmission or temp files.
func (stg *Storage) Cleanup() {
	files, err := os.ReadDir(stg.folder)
	if err != nil {
		return
	}

	for _, file := range files {
		if !file.IsDir() {
			if strings.HasSuffix(file.Name(), ".tmp") {
				info, err := file.Info()
				if err == nil && time.Since(info.ModTime()) > time.Duration(5)*time.Minute {
					_ = os.Remove(file.Name())
				}
			} else if strings.HasSuffix(file.Name(), transmissionFileExtension) {
				if _, ok := stg.parseFilename(file.Name()); !ok {
					_ = os.Remove(file.Name())
				}
			}
		}
	}
}

func (stg *Storage) Save(payload string) error {
	return stg.save(time.Duration(0), 0, payload)
}

func (stg *Storage) SaveRetry(payload string, delayDuration time.Duration, retryCount int) error {
	return stg.save(delayDuration, retryCount, payload)
}

func (stg *Storage) save(delayDuration time.Duration, retryCount int, payload string) error {
	file, err := os.CreateTemp(stg.folder, "*_azdtrans.tmp")
	if err != nil {
		return fmt.Errorf("failed to create transmission file :%v", err)
	}

	err = os.WriteFile(file.Name(), []byte(payload), 0644)
	if err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}
	file.Close()

	generatedFileName := filepath.Base(file.Name())
	randomSuffix := generatedFileName[:strings.LastIndex(generatedFileName, "_")]
	transmitTime := time.Now().Add(delayDuration)
	fileName := fmt.Sprintf("%s_%d_%s.trn", transmitTime.Format(transmissionFileFormat), retryCount, randomSuffix)
	err = os.Rename(file.Name(), filepath.Join(stg.folder, fileName))
	if err != nil {
		return fmt.Errorf("failed to rename file: %v", err)
	}

	return nil
}

func (stg *Storage) Remove(transmission *StoredTransmission) error {
	err := os.Remove(transmission.fileName)
	if err != nil {
		return fmt.Errorf("failed to remove stored trx: %v", err)
	}

	return nil
}

// Gets the latest stored transmission ready for transmission.
// Returns nil if no transmissions exist.
// Returns error if an error occurs while reading storage.
func (stg *Storage) GetLatestTransmission() (*StoredTransmission, error) {
	files, err := stg.getAllFiles()
	if err != nil {
		return nil, fmt.Errorf("failed to get trx files: %v", err)
	}

	latestTime := time.Time{}
	latestIndex := -1
	now := time.Now()
	for i, file := range files {
		if file.timestamp.After(now) && file.timestamp.After(latestTime) {
			latestTime = file.timestamp
			latestIndex = i
		}
	}

	if latestIndex == -1 {
		return nil, nil
	}

	file := files[latestIndex]
	payload, err := os.ReadFile(file.name)
	if err != nil {
		return nil, fmt.Errorf("failed to read trx file: %v", err)
	}

	return &StoredTransmission{
		fileName:   file.name,
		retryCount: file.retryCount,
		payload:    string(payload),
	}, nil
}

func (stg *Storage) getAllFiles() ([]transmissionFileEntry, error) {
	dirEntries, err := readDir(stg.folder)
	if err != nil {
		return nil, fmt.Errorf("error reading folder: %v", err)
	}

	files := []transmissionFileEntry{}

	for _, entry := range dirEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), transmissionFileExtension) {
			name := entry.Name()
			entry, ok := stg.parseFilename(name)

			if ok {
				files = append(files, *entry)
			}
		}
	}

	return files, nil
}

// readDir is os.ReadDir except it returns entries in directory order instead of by name order
func readDir(name string) ([]os.DirEntry, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	dirs, err := f.ReadDir(-1)
	return dirs, err
}

func (stg *Storage) parseFilename(name string) (*transmissionFileEntry, bool) {
	if !strings.HasSuffix(name, transmissionFileExtension) {
		return nil, false
	}

	sections := strings.Split(name, "_")
	if len(sections) < 2 {
		return nil, false
	}

	timestamp, err := time.Parse(transmissionFileFormat, sections[0])
	if err != nil {
		return nil, false
	}

	retryCount, err := strconv.Atoi(sections[1])
	if err != nil {
		return nil, false
	}

	return &transmissionFileEntry{
		name:       filepath.Join(stg.folder, name),
		timestamp:  timestamp,
		retryCount: retryCount,
	}, true
}

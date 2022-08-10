package telemetry

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const logDirectoryPermissions = 0755
const fileExtension = "trn"
const transmissionFileFormat = "20060102T150405"
const defaultCleanupIntervalSeconds = 10

type Storage struct {
	folder          string
	cleanUpInterval time.Duration
	abortChan       chan (struct{})
}

type Transmission struct {
	// Timestamp after which the transmission can be transmitted
	timestamp time.Time

	// Number of retries attempted
	retryCount int

	// Payload of the transmission
	payload string
}

type transmissionFileEntry struct {
	name       string
	timestamp  time.Time
	retryCount int
}

func NewStorage(folder string) *Storage {
	storage := Storage{
		folder:          folder,
		cleanUpInterval: defaultCleanupIntervalSeconds * time.Second,
	}
	storage.init()
	return &storage
}

func (stg *Storage) init() error {
	if err := os.MkdirAll(filepath.Dir(stg.folder), logDirectoryPermissions); err != nil {
		return fmt.Errorf("failed to create telemetry storage folder: %v", err)
	}

	go func() {
		stg.cleanup()
	}()

	return nil
}

func (stg *Storage) StartCleanup() {
	go stg.cleanup()
}

func (stg *Storage) StopCleanup() {
	stg.abortChan <- struct{}{}
}

func (stg *Storage) cleanup() {
	for {
		select {
		case <-time.After(stg.cleanUpInterval):
			files, err := os.ReadDir(stg.folder)
			if err != nil {
				continue
			}

			for _, file := range files {
				if !file.IsDir() && !strings.HasSuffix(file.Name(), "."+fileExtension) {
					_ = os.Remove(file.Name())
				}
			}

			time.Sleep(stg.cleanUpInterval)

		case <-stg.abortChan:
			return
		}
	}
}

// Saves the transmission on storage.
func (stg *Storage) SaveTransmission(trn *Transmission) error {
	file, err := os.CreateTemp(stg.folder, "*_azdtrans.tmp")
	if err != nil {
		return fmt.Errorf("failed to create transmission file :%v", err)
	}

	err = os.WriteFile(file.Name(), []byte(trn.payload), 0644)
	if err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}

	generatedFileName := filepath.Base(file.Name())
	randomPrefix := generatedFileName[:strings.Index(generatedFileName, "_")]
	fileName := fmt.Sprintf("%s_%d_%s.trn", trn.timestamp.Local().Format(transmissionFileFormat), trn.retryCount, randomPrefix)
	err = os.Rename(file.Name(), filepath.Join(stg.folder, fileName))
	if err != nil {
		return fmt.Errorf("failed to rename file: %v", err)
	}

	return nil
}

// Gets the latest stored transmission.
// Returns nil if no transmissions exist.
// Returns error if an error occurs while reading storage.
func (stg *Storage) GetLatestTransmission() (*Transmission, error) {
	files, err := stg.getAllFiles()
	if err != nil {
		return nil, fmt.Errorf("failed to get trx files: %v", err)
	}

	if len(files) == 0 {
		return nil, nil
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].timestamp.Before(files[j].timestamp)
	})

	payload, err := os.ReadFile(files[0].name)
	if err != nil {
		return nil, fmt.Errorf("failed to read trx file: %v", err)
	}

	return &Transmission{
		timestamp:  files[0].timestamp,
		retryCount: files[0].retryCount,
		payload:    string(payload),
	}, nil
}

func (stg *Storage) getAllFiles() ([]transmissionFileEntry, error) {
	dirEntries, err := os.ReadDir(stg.folder)
	if err != nil {
		return nil, fmt.Errorf("error reading folder: %v", err)
	}

	files := []transmissionFileEntry{}

	for _, entry := range dirEntries {
		name := entry.Name()

		if strings.HasSuffix(name, "."+fileExtension) {
			sections := strings.Split(name, "_")
			if len(sections) < 2 {
				// Invalid file, remove
				os.Remove(filepath.Join(stg.folder, name))
			}

			timestamp, err := time.Parse(transmissionFileFormat, sections[0])
			if err != nil {
				// Invalid file, remove
				os.Remove(filepath.Join(stg.folder, name))
			}

			retryCount, err := strconv.Atoi(sections[1])
			if err != nil {
				// Invalid file, remove
				os.Remove(filepath.Join(stg.folder, name))
			}

			files = append(files, transmissionFileEntry{
				name:       filepath.Join(stg.folder, name),
				timestamp:  timestamp.Local(),
				retryCount: retryCount,
			})
		}
	}

	return files, nil
}

package telemetry

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewStorage(t *testing.T) {
	folder := filepath.Join(os.TempDir(), "azdnewstg")

	t.Run("CreatesFolder", func(t *testing.T) {
		err := os.RemoveAll(folder)
		assert.NoError(t, err)

		storage, err := NewStorage(folder)
		assert.NoError(t, err)
		assert.DirExists(t, storage.folder)
		os.RemoveAll(folder)
	})

	t.Run("HandlesExistingFolder", func(t *testing.T) {
		err := os.Mkdir(folder, 644)
		assert.NoError(t, err)

		storage, err := NewStorage(folder)
		assert.NoError(t, err)
		assert.DirExists(t, storage.folder)

		os.RemoveAll(folder)
	})
}

func TestSaveTransmission(t *testing.T) {
	folder := filepath.Join(os.TempDir(), "azdsave")
	timeUtc, _ := time.Parse(time.RFC3339, "2006-01-02T15:04:05Z")
	_, offset := time.Now().Zone()
	fixedZone := time.FixedZone("Offset", offset+int(1*time.Hour/time.Second))

	tests := []struct {
		title string
		trn   *Transmission
	}{
		{
			title: "Basic",
			trn: &Transmission{
				timestamp:  timeUtc,
				retryCount: 0,
				payload:    "SOME_DATA_HERE\r\n",
			},
		},
		{
			title: "Timezone",
			trn: &Transmission{
				timestamp:  time.Date(2006, 1, 2, 13, 4, 5, 0, fixedZone),
				retryCount: 0,
				payload:    "SOME_DATA_HERE",
			},
		},
		{
			title: "Extended",
			trn: &Transmission{
				timestamp:  timeUtc,
				retryCount: 15,
				payload:    "Line1\nLine2\nLine3",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.title, func(t *testing.T) {
			setupErr := os.RemoveAll(folder)
			assert.NoError(t, setupErr)

			storage := NewStorage(folder)
			err := storage.SaveTransmission(test.trn)
			assert.NoError(t, err)

			trxs, err := storage.getAllFiles()
			assert.NoError(t, err)
			assert.Len(t, trxs, 1)

			prefix := fmt.Sprintf("%s_%d", test.trn.timestamp.Format(transmissionFileFormat), test.trn.retryCount)
			assert.Regexp(t, regexp.MustCompile(prefix+"_\\d+\\.trn"), filepath.Base(trxs[0].name))
			assert.Equal(t, test.trn.timestamp.Unix(), trxs[0].timestamp.Unix())
			assert.Equal(t, test.trn.retryCount, trxs[0].retryCount)

			bytes, err := os.ReadFile(trxs[0].name)
			assert.NoError(t, err)
			assert.Equal(t, test.trn.payload, string(bytes))

			storage.GetLatestTransmission()

		})
	}
	os.RemoveAll(folder)
}

func TestGetLatestTransmissionNoTransmissionExists(t *testing.T) {
	folder := filepath.Join(os.TempDir(), "azdnotrx")
	os.RemoveAll(folder)

	storage := NewStorage(folder)
	trn, err := storage.GetLatestTransmission()
	assert.NoError(t, err)
	assert.Nil(t, trn)
}

func TestGetLatestTransmission(t *testing.T) {
	timeSnapshot := time.Date(2006, 1, 2, 13, 4, 5, 0, time.UTC)
	trx := []Transmission{
		{
			timestamp:  timeSnapshot.Add(time.Duration(2) * time.Second),
			retryCount: 0,
			payload:    "Latest",
		},
		{
			timestamp:  timeSnapshot.Add(time.Duration(1) * time.Second),
			retryCount: 0,
			payload:    "Later",
		},
		{
			timestamp:  timeSnapshot,
			retryCount: 0,
			payload:    "Earliest",
		},
	}

	folder := filepath.Join(os.TempDir(), "azdtrx")
	os.RemoveAll(folder)

	storage := NewStorage(folder)
	for _, trn := range trx {
		storage.SaveTransmission(&trn)
	}

	trn, err := storage.GetLatestTransmission()
	assert.NoError(t, err)
	assert.Equal(t, trx[0], trn)
	os.RemoveAll(folder)

}

func TestCleanup(t *testing.T) {
	folder := filepath.Join(os.TempDir(), "azdtrx")
	os.RemoveAll(folder)

	invalidFiles := []string{
		"invalid_file",
		"invalid_file.txt",
		"invalid_file.log",
		"invalidformat.trn",
		"notadate_1.trn",
		transmissionFileFormat + "_notanumber.trn",
	}

	validFiles := []string{
		fmt.Sprintf("%s_0.trn", getUtcTime().Format(transmissionFileFormat)),
		fmt.Sprintf("%s_0.trn", getLocalTime().Format(transmissionFileFormat)),
		fmt.Sprintf("%s_0.trn", getNonLocalTime().Format(transmissionFileFormat)),
		fmt.Sprintf("%s_1.trn", getLocalTime().Format(transmissionFileFormat)),
		fmt.Sprintf("%s_1_1234.trn", getLocalTime().Format(transmissionFileFormat)),
		fmt.Sprintf("%s_3_unique.trn", getLocalTime().Format(transmissionFileFormat)),
	}

	files := append(invalidFiles, validFiles...)

	storage := NewStorage(folder)

	for _, file := range files {
		_, err := os.Create(filepath.Join(folder, file))
		assert.NoError(t, err)
	}

	storage.cleanup()

	remainingFiles, err := os.ReadDir(storage.folder)
	assert.NoError(t, err)
	assert.Len(t, remainingFiles, len(validFiles))
	for _, remainingFile := range remainingFiles {
		assert.Contains(t, validFiles, remainingFile.Name())
	}
}

func getUtcTime() time.Time {
	return time.Date(2006, 1, 2, 13, 4, 5, 0, time.UTC)
}

func getLocalTime() time.Time {
	return time.Date(2006, 1, 2, 13, 4, 5, 0, time.Local)
}

func getNonLocalTime() time.Time {
	_, offset := time.Now().Zone()
	fixedZone := time.FixedZone("Offset", offset+int(1*time.Hour/time.Second))

	return time.Date(2006, 1, 2, 13, 4, 5, 0, fixedZone)
}

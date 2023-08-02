package runcontext

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

// firstRunFileName is the name of the file created in the azd configuration
// directory after the first run of the CLI. It's presence is used to determine
// if this is the first run of the CLI.
const cFirstRunFileName = "first-run"

func IsFirstRun() bool {
	firstRunFilePath, err := getFirstRunFilePath()
	if err != nil {
		log.Printf("failed to get first run file path: %v", err)
		// If the first run file path can't be resolved assume it is the first
		// run and return true
		return true
	}

	if _, err := os.Stat(firstRunFilePath); err == nil {
		// First run file exists, this is not the first run
		return false
	} else if errors.Is(err, fs.ErrNotExist) {
		// The file does not exist, this is the first run
		return true
	} else {
		log.Printf("failed to stat first run file: %v", err)
		// If the first run file can't be read assume it is the first run and
		// return true
		return true
	}
}

func getFirstRunFilePath() (string, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		log.Printf("failed to get user config dir: %v", err)
		return "", err
	}

	return filepath.Join(configDir, cFirstRunFileName), nil
}

func SetupFirstRun() error {
	firstRunFilePath, err := getFirstRunFilePath()
	if err != nil {
		return err
	}

	_, err = os.Create(firstRunFilePath)
	if err != nil {
		return err
	}

	return nil
}

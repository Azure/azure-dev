package telemetry

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/internal/runcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

// Telemetry notice text displayed to the user in some scenarios
//
//nolint:lll
const cTelemetryNoticeText = `The Azure Developer CLI collects usage data and sends that usage data to Microsoft in order to help us improve your experience.
You can opt-out of telemetry by setting the AZURE_DEV_COLLECT_TELEMETRY environment variable to 'no' in the shell you use.

Read more about Azure Developer CLI telemetry: https://github.com/Azure/azure-dev#data-collection`

// The name of the file created in the azd configuration directory after the
// first run of the CLI. It's presence is used to determine if this is the
// first run of the CLI.
const cFirstRunFileName = "first-run"

func FirstNotice() string {
	// If the AZURE_DEV_COLLECT_TELEMETRY environment variable is set to any
	// value, don't display the telemetry notice. The user has either opted into
	// or out of telemetry already.
	if _, has := os.LookupEnv(collectTelemetryEnvVar); has {
		return ""
	}

	// First run is only displayed when running in Cloud Shell
	if runcontext.IsRunningInCloudShell() && !noticeShown() {
		err := SetupFirstRun()
		if err != nil {
			log.Printf("failed to setup first run: %v", err)
		}

		return cTelemetryNoticeText
	}

	return ""
}

func noticeShown() bool {
	firstRunFilePath, err := getFirstRunFilePath()
	if err != nil {
		log.Printf("failed to get first run file path: %v", err)
		// Assume no notice has been show
		return false
	}

	if _, err := os.Stat(firstRunFilePath); err == nil {
		// First run file exists, this is not the first run
		return true
	} else if errors.Is(err, fs.ErrNotExist) {
		// The file does not exist, this is the first run
		return false
	} else {
		log.Printf("failed to stat first run file: %v", err)
		// If the first run file can't be read assume notice hasn't been shown
		return false
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

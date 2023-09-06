package telemetry

import (
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/assert"
)

func setupSuite(withFirstRunFile bool, t *testing.T) func(t *testing.T) {
	firstRunFilePath, err := getFirstRunFilePath()
	if err != nil {
		t.Fatalf("failed to get first run file path: %v", err)
	}

	var tmpFilename string
	// If the first-run file exists
	if noticeShown() {

		// Move the first-run file out of the way but keep its local contents
		// (if any), content can be restored on teardown.
		if !withFirstRunFile {
			file, err := os.CreateTemp("", "azd-test-")
			if err != nil {
				t.Fatalf("failed to create temp file: %v", err)
			}
			file.Close()

			tmpFilename = file.Name()
			err = os.Rename(firstRunFilePath, tmpFilename)
			if err != nil {
				t.Fatalf("failed to rename first run file: %v", err)
			}

			return func(t *testing.T) {
				err = os.Rename(tmpFilename, firstRunFilePath)
				if err != nil {
					t.Fatalf("failed to rename first run file: %v", err)
				}
			}
		}
	} else {
		if withFirstRunFile {
			// Create a first-run file to simulate the existence of a first-run
			// file, remove the created file on teardown.
			file, err := os.Create(firstRunFilePath)
			if err != nil {
				t.Fatalf("failed to create first run file: %v", err)
			}
			file.Close()

			return func(t *testing.T) {
				err = os.Remove(firstRunFilePath)
				if err != nil {
					t.Fatalf("failed to remove first run file: %v", err)
				}
			}
		}
	}

	// No setup or teardown required
	return func(t *testing.T) {}
}

func Test_FirstNotice(t *testing.T) {
	t.Run("in Cloud Shell", func(t *testing.T) {
		ostest.Setenv(t, "AZD_IN_CLOUDSHELL", "1")

		t.Run("returns nothing if opted into telemetry", func(t *testing.T) {
			teardown := setupSuite(false, t)
			defer teardown(t)

			ostest.Setenv(t, collectTelemetryEnvVar, "yes")
			assert.Empty(t, FirstNotice(), "should not display telemetry notice if opted in")
		})

		t.Run("returns nothing if opted out of telemetry", func(t *testing.T) {
			teardown := setupSuite(false, t)
			defer teardown(t)

			ostest.Setenv(t, collectTelemetryEnvVar, "no")
			assert.Empty(t, FirstNotice(), "should not display telemetry notice if opted out")
		})

		t.Run("returns nothing if first run file exists", func(t *testing.T) {
			teardown := setupSuite(true, t)
			defer teardown(t)

			assert.Empty(t, FirstNotice(), "should not display telemetry notice if first run file exists")
		})
	})

	t.Run("not in Cloud Shell", func(t *testing.T) {
		ostest.Unsetenv(t, "AZD_IN_CLOUDSHELL")

		t.Run("returns nothing if first run file doesn't exist", func(t *testing.T) {
			teardown := setupSuite(false, t)
			defer teardown(t)

			assert.Empty(t, FirstNotice(), "should not display telemetry notice if first run file doesn't exist")
		})

		t.Run("returns nothing if opted into telemetry", func(t *testing.T) {
			teardown := setupSuite(false, t)
			defer teardown(t)

			ostest.Setenv(t, collectTelemetryEnvVar, "yes")
			assert.Empty(t, FirstNotice(), "should not display telemetry notice if opted in")
		})

		t.Run("returns nothing if opted out of telemetry", func(t *testing.T) {
			teardown := setupSuite(false, t)
			defer teardown(t)

			ostest.Setenv(t, collectTelemetryEnvVar, "no")
			assert.Empty(t, FirstNotice(), "should not display telemetry notice if opted out")
		})
	})
}

package terraform

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// creates a copy of azd binary and renames it to az and returns the path to it
func emulateAzFromPath() (string, error) {
	path, err := exec.LookPath("azd")
	if err != nil {
		return "", fmt.Errorf("azd binary not found in PATH: %w", err)
	}
	azdConfigPath, err := config.GetUserConfigDir()
	if err != nil {
		return "", fmt.Errorf("could not get user config dir: %w", err)
	}
	emuPath, err := os.MkdirTemp(filepath.Join(azdConfigPath, "bin"), "azEmulate")
	if err != nil {
		return "", fmt.Errorf("could not create directory for azEmulate: %w", err)
	}
	err = os.MkdirAll(emuPath, osutil.PermissionDirectoryOwnerOnly)
	if err != nil {
		return "", fmt.Errorf("could not create directory for azEmulate: %w", err)
	}
	emuPath = filepath.Join(emuPath, strings.ReplaceAll(filepath.Base(path), "azd", "az"))

	srcFile, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("opening src: %w", err)
	}
	defer srcFile.Close()

	destFile, err := os.OpenFile(emuPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return "", fmt.Errorf("creating dest: %w", err)
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, srcFile)
	if err != nil {
		return "", fmt.Errorf("copying binary: %w", err)
	}

	return emuPath, nil
}

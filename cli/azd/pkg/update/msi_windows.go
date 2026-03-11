// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package update

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

const (
	// Windows process creation flags for detaching msiexec from the parent process.
	windowsCreateNewProcessGroup = 0x00000200
	windowsDetachedProcess       = 0x00000008
)

// newDetachedSysProcAttr returns SysProcAttr that detaches the child process
// so it survives after the parent (azd) exits.
func newDetachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: windowsCreateNewProcessGroup | windowsDetachedProcess,
	}
}

// msiLogFilePath returns the path for the MSI verbose install log (~/.azd/logs/msi-update.log).
func msiLogFilePath() (string, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return "", err
	}

	logsDir := filepath.Join(configDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return "", err
	}

	return filepath.Join(logsDir, "msi-update.log"), nil
}

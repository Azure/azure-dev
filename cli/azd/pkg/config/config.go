// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package config provides functionality related to storing application-wide configuration data.
//
// Configuration data stored should not be specific to a given repository/project.
package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

const configDir = ".azd"

// GetUserConfigDir returns the config directory for storing user wide configuration data.
//
// The config directory is guaranteed to exist, otherwise an error is returned.
func GetUserConfigDir() (string, error) {
	user, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("could not determine current user: %w", err)
	}

	configDirPath := filepath.Join(user.HomeDir, configDir)
	err = os.MkdirAll(configDirPath, osutil.PermissionDirectory)

	return configDirPath, err
}

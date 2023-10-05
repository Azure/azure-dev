package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

const cConfigDir = ".azd"

// Config Manager provides the ability to load, parse and save azd configuration files
type manager struct {
}

type Manager interface {
	Save(config Config, writer io.Writer) error
	Load(io.Reader) (Config, error)
}

// Creates a new Configuration Manager
func NewManager() Manager {
	return &manager{}
}

// Saves the azd configuration to the specified file path
func (c *manager) Save(config Config, writer io.Writer) error {
	configJson, err := json.MarshalIndent(config.Raw(), "", "  ")
	if err != nil {
		return fmt.Errorf("failed marshalling config JSON: %w", err)
	}

	_, err = writer.Write(configJson)
	if err != nil {
		return fmt.Errorf("failed writing configuration data: %w", err)
	}

	return nil
}

// Loads azd configuration from the specified file path
func (c *manager) Load(reader io.Reader) (Config, error) {
	jsonBytes, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed reading azd configuration file")
	}

	return Parse(jsonBytes)
}

// Parses azd configuration JSON and returns a Config instance
func Parse(configJson []byte) (Config, error) {
	var data map[string]any
	err := json.Unmarshal(configJson, &data)
	if err != nil {
		return nil, fmt.Errorf("failed unmarshalling configuration JSON: %w", err)
	}

	return NewConfig(data), nil
}

// GetUserConfigDir returns the config directory for storing user wide configuration data.
//
// The config directory is guaranteed to exist, otherwise an error is returned.
func GetUserConfigDir() (string, error) {
	configDirPath := os.Getenv("AZD_CONFIG_DIR")
	if configDirPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("could not determine current home directory: %w", err)
		}

		configDirPath = filepath.Join(homeDir, cConfigDir)
	}

	err := os.MkdirAll(configDirPath, osutil.PermissionDirectoryOwnerOnly)
	if err != nil {
		return configDirPath, err
	}

	// Ensure that the "x" permission is set on the folder for the current
	// user. In cases where the config directory is ~/.azd, OS upgrades and
	// other processes can remove the "x" permission
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		info, err := os.Stat(configDirPath)
		if err != nil {
			return configDirPath, err
		}

		permissions := info.Mode().Perm()
		if permissions&osutil.PermissionMaskDirectoryExecute == 0 {
			// Ensure user execute permissions
			err := os.Chmod(configDirPath, permissions|osutil.PermissionMaskDirectoryExecute)
			return configDirPath, err
		}
	}

	return configDirPath, err
}

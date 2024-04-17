package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/google/uuid"
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
	paramsKey := "infra.parameters"
	_, exists := config.Get(paramsKey)
	if !exists {
		// no infra parameters, so this is user config, no need to save secrets
		configJson, err := json.MarshalIndent(config.Raw(), "", "  ")
		if err != nil {
			return fmt.Errorf("failed marshalling config JSON: %w", err)
		}
		_, err = writer.Write(configJson)
		return err
	}

	// config has infra.parameters. Handle secrets
	configPaths := config.Paths()
	secretKeys := config.SecretKeys()
	sanitizedConfig := NewEmptyConfig()
	secrets := make(map[string]any)

	for _, path := range configPaths {
		value, _ := config.Get(path)
		if _, isSecret := secretKeys[path]; !isSecret {
			// none secrets are directly copied
			if err := sanitizedConfig.Set(path, value); err != nil {
				return fmt.Errorf("failed setting config value: %w", err)
			}
			continue
		}
		secrets[path] = value
	}

	if len(secrets) > 0 {
		userConfigManager := NewUserConfigManager(NewFileConfigManager(c))
		uConfig, err := userConfigManager.Load()
		if err != nil {
			return fmt.Errorf("failed loading user config: %w", err)
		}

		uConfigSecretsNode, exists := uConfig.Get("secrets")
		var uConfigSecretsMap map[string]any
		if exists {
			castMap, ok := uConfigSecretsNode.(map[string]any)
			if !ok {
				return fmt.Errorf("failed to convert secrets to map")
			}
			uConfigSecretsMap = castMap
		}
		updateUserConfig := false
		for path, value := range secrets {
			var existingSecretKey string
			for sKey, existingValue := range uConfigSecretsMap {
				if existingValue == value {
					existingSecretKey = sKey
					break
				}
			}
			if existingSecretKey == "" {
				updateUserConfig = true
				secretUUID := uuid.New().String()
				if err := sanitizedConfig.Set(path, secretUUID); err != nil {
					return fmt.Errorf("failed setting config value: %w", err)
				}
				if err := uConfig.Set("secrets."+secretUUID, value); err != nil {
					return fmt.Errorf("failed setting user config value: %w", err)
				}
			} else {
				if err := sanitizedConfig.Set(path, existingSecretKey); err != nil {
					return fmt.Errorf("failed setting config value: %w", err)
				}
			}
		}
		if updateUserConfig {
			if err := userConfigManager.Save(uConfig); err != nil {
				return fmt.Errorf("failed saving user config: %w", err)
			}
		}
	}

	configJson, err := json.MarshalIndent(sanitizedConfig.Raw(), "", "  ")
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

	config, err := Parse(jsonBytes)
	if err != nil {
		return nil, fmt.Errorf("failed parsing configuration: %w", err)
	}

	// secrets resolution for infra secured params
	paramsKey := "infra.parameters"
	paramsNode, exists := config.Get(paramsKey)
	if !exists {
		return config, nil
	}
	// found `infra.parameters`, so this is azd config, it is save to read user config
	userConfigManager := NewUserConfigManager(NewFileConfigManager(c))
	uConfig, err := userConfigManager.Load()
	if err != nil {
		return nil, fmt.Errorf("failed loading user config: %w", err)
	}
	secretsKey := "secrets"
	secretsNode, exists := uConfig.Get(secretsKey)
	if !exists {
		return config, nil
	}
	secretsMap, ok := secretsNode.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("failed to convert secrets to map")
	}
	paramsMap, ok := paramsNode.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("failed to convert parameters to map")
	}
	for key, value := range paramsMap {
		secretId, castOk := value.(string)
		if !castOk {
			return nil, fmt.Errorf("failed to convert parameter value to string")
		}
		if secretValue, isSecret := secretsMap[secretId]; isSecret {
			if err := config.SetSecret(fmt.Sprintf("%s.%s", paramsKey, key), secretValue); err != nil {
				return nil, fmt.Errorf("failed setting secret value: %w", err)
			}
		}
	}

	return config, nil
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

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

const infraParametersKey = "infra.parameters"
const vaultIdKey = "vaultId"

// Saves the azd configuration to the specified file path
func (c *manager) Save(config Config, writer io.Writer) error {
	_, hasInfraParams := config.Get(infraParametersKey)
	secretKeys := config.SecretKeys()
	if !hasInfraParams || len(secretKeys) == 0 {
		// no infra parameters, or no secrets in config
		configJson, err := json.MarshalIndent(config.Raw(), "", "  ")
		if err != nil {
			return fmt.Errorf("failed marshalling config JSON: %w", err)
		}
		_, err = writer.Write(configJson)
		return err
	}

	// config has infra.parameters. Handle secrets
	configPaths := config.Paths()
	withNoSecretsConfig := NewEmptyConfig()
	configSecrets := make(map[string]any)

	// split config into secrets and non-secrets
	for _, path := range configPaths {
		value, _ := config.Get(path)
		if _, isSecret := secretKeys[path]; !isSecret {
			if err := withNoSecretsConfig.Set(path, value); err != nil {
				return fmt.Errorf("failed setting config value: %w", err)
			}
			continue
		}
		configSecrets[path] = value
	}

	userSecretsManager := newUserSecretsManager(NewFileConfigManager(c))
	userVault, err := userSecretsManager.Load()
	if err != nil {
		return fmt.Errorf("failed loading user vault: %w", err)
	}

	vaultIdNode, vaultExists := config.Get(vaultIdKey)
	var vaultId string
	if vaultExists {
		vaultIdCast, castOk := vaultIdNode.(string)
		if !castOk {
			return fmt.Errorf("failed casting vault id to string")
		}
		vaultId = vaultIdCast
	} else {
		vaultUuid, err := uuid.NewRandom()
		if err != nil {
			return fmt.Errorf("failed generating vault id: %w", err)
		}
		vaultId = vaultUuid.String()
	}
	// Set in memory only, no need to persist this as we will persist it with withNoSecretsConfig
	if err := config.Set(vaultIdKey, vaultId); err != nil {
		return fmt.Errorf("failed setting vault id in config: %w", err)
	}
	if err := withNoSecretsConfig.Set(vaultIdKey, vaultId); err != nil {
		return fmt.Errorf("failed setting vault id in config: %w", err)
	}

	if err := userVault.Set(vaultId, configSecrets); err != nil {
		return fmt.Errorf("failed setting secrets in vault: %w", err)
	}
	if err := userSecretsManager.Save(userVault); err != nil {
		return fmt.Errorf("failed saving user vault: %w", err)
	}

	configJson, err := json.MarshalIndent(withNoSecretsConfig.Raw(), "", "  ")
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

	// secrets resolution based on vaultId
	var vaultId string
	hasVaultId, err := config.GetSection(vaultIdKey, &vaultId)
	if err != nil {
		return nil, fmt.Errorf("failed getting vault id: %w", err)
	}
	if !hasVaultId {
		return config, nil
	}

	userSecretsManager := newUserSecretsManager(NewFileConfigManager(c))
	userSecretsConfig, err := userSecretsManager.Load()
	if err != nil {
		return nil, fmt.Errorf("failed loading user config: %w", err)
	}
	var vault map[string]any
	hasVault, err := userSecretsConfig.GetSection(vaultId, &vault)
	if err != nil {
		return nil, fmt.Errorf("failed getting vault: %w", err)
	}
	if !hasVault {
		return nil, fmt.Errorf("vault not found")
	}

	for key, value := range vault {
		if err := config.SetSecret(key, value); err != nil {
			return nil, fmt.Errorf("failed setting secret value: %w", err)
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

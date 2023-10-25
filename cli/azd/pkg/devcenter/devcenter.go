package devcenter

import (
	"encoding/json"
	"fmt"
)

var ModeConfigPath = fmt.Sprintf("%s.mode", ConfigPath)

// Merges supplemental configuration into the base config only if the key/value doesn't already exist in the base config
// Example: If the base config is a fully configured object, then any supplemental configuration will be ignored
func MergeConfigs(configs ...*Config) *Config {
	if len(configs) == 0 {
		panic("no configs provided")
	}

	destConfig := configs[0]

	mergedConfig := &Config{
		Name:                  destConfig.Name,
		Catalog:               destConfig.Catalog,
		Project:               destConfig.Project,
		EnvironmentType:       destConfig.EnvironmentType,
		EnvironmentDefinition: destConfig.EnvironmentDefinition,
	}

	for _, config := range configs[1:] {
		if config == nil {
			continue
		}

		if config.Name != "" && mergedConfig.Name == "" {
			mergedConfig.Name = config.Name
		}

		if config.Catalog != "" && mergedConfig.Catalog == "" {
			mergedConfig.Catalog = config.Catalog
		}

		if config.Project != "" && mergedConfig.Project == "" {
			mergedConfig.Project = config.Project
		}

		if config.EnvironmentType != "" && mergedConfig.EnvironmentType == "" {
			mergedConfig.EnvironmentType = config.EnvironmentType
		}

		if config.EnvironmentDefinition != "" && mergedConfig.EnvironmentDefinition == "" {
			mergedConfig.EnvironmentDefinition = config.EnvironmentDefinition
		}
	}

	return mergedConfig
}

func ParseConfig(partialConfig any) (*Config, error) {
	var config *Config

	jsonBytes, err := json.Marshal(partialConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal devCenter configuration: %w", err)
	}

	if err := json.Unmarshal(jsonBytes, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal devCenter configuration: %w", err)
	}

	return config, nil
}

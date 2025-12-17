// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"log"

	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/braydonk/yaml"
)

// ConfigOption defines a configuration setting that can be set in azd config
type ConfigOption struct {
	Key           string   `yaml:"key"`
	Description   string   `yaml:"description"`
	Type          string   `yaml:"type"`
	AllowedValues []string `yaml:"allowedValues,omitempty"`
	Example       string   `yaml:"example,omitempty"`
	EnvVar        string   `yaml:"envVar,omitempty"`
}

var allConfigOptions []ConfigOption

func init() {
	err := yaml.Unmarshal(resources.ConfigOptions, &allConfigOptions)
	if err != nil {
		log.Panicf("Can't unmarshal config options! %v", err)
	}
}

// GetAllConfigOptions returns all available configuration options
func GetAllConfigOptions() []ConfigOption {
	return allConfigOptions
}

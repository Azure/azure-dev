// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import "github.com/azure/azure-dev/cli/azd/pkg/config"

// Env exposes environment variables and configuration to consumers.
// Storage and backing-state operations use BackingEnv.
type Env interface {
	// BackingEnv returns the underlying environment state.
	// It may be a layer-local snapshot rather than the shared environment.
	BackingEnv() *Environment

	Name() string
	Getenv(key string) string
	LookupEnv(key string) (string, bool)
	Dotenv() map[string]string
	DotenvSet(key, value string)
	DotenvDelete(key string)
	Environ() []string
	GetSubscriptionId() string
	SetSubscriptionId(id string)
	GetTenantId() string
	GetLocation() string
	SetLocation(location string)
	GetServiceProperty(serviceName, propertyName string) string
	SetServiceProperty(serviceName, propertyName, value string)
	GetConfig() config.Config
}

var _ Env = (*Environment)(nil)

// BackingEnv returns this environment.
func (e *Environment) BackingEnv() *Environment {
	return e
}

// GetConfig returns the environment configuration.
func (e *Environment) GetConfig() config.Config {
	return e.Config
}

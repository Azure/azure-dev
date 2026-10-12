// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"fmt"
	"maps"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

// ScopedEnvironment exposes environment variables and configuration to provisioning
// providers and their environment-dependent helpers. Other consumers use *Environment.
// Storage and backing-state operations use BackingEnv.
// Provisioning overrides apply to providers and helpers that resolve their inputs.
// Other consumers use the concrete Environment supplied by their caller.
type ScopedEnvironment interface {
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

var _ ScopedEnvironment = (*Environment)(nil)

// BackingEnv returns this environment.
func (e *Environment) BackingEnv() *Environment {
	return e
}

// GetConfig returns the environment configuration.
func (e *Environment) GetConfig() config.Config {
	return e.Config
}

type mappedScopedEnvironment struct {
	ScopedEnvironment
	aliases map[string]string
}

// NewMappedScopedEnvironment translates variable names between the provider view and the project view.
// Aliases use provider names as keys and project names as values for reads, writes, and deletes.
func NewMappedScopedEnvironment(
	env ScopedEnvironment,
	aliases map[string]string,
) ScopedEnvironment {
	if len(aliases) == 0 {
		return env
	}

	return &mappedScopedEnvironment{
		ScopedEnvironment: env,
		aliases:           aliases,
	}
}

func (e *mappedScopedEnvironment) Getenv(key string) string {
	return e.ScopedEnvironment.Getenv(e.projectKey(key))
}

func (e *mappedScopedEnvironment) LookupEnv(key string) (string, bool) {
	return e.ScopedEnvironment.LookupEnv(e.projectKey(key))
}

func (e *mappedScopedEnvironment) Dotenv() map[string]string {
	values := e.ScopedEnvironment.Dotenv()

	// Resolve every project variable before adding provider names so overlapping aliases do not affect each other.
	mapped := make(map[string]string, len(e.aliases))
	for localName, sharedName := range e.aliases {
		// Aliases must not restore loader controls excluded from the backing dotenv.
		if isLoaderControlKey(localName) {
			continue
		}
		mapped[localName], _ = e.ScopedEnvironment.LookupEnv(sharedName)
	}
	maps.Copy(values, mapped)

	return values
}

func (e *mappedScopedEnvironment) DotenvSet(key string, value string) {
	e.ScopedEnvironment.DotenvSet(e.projectKey(key), value)
}

func (e *mappedScopedEnvironment) DotenvDelete(key string) {
	e.ScopedEnvironment.DotenvDelete(e.projectKey(key))
}

func (e *mappedScopedEnvironment) Environ() []string {
	values := e.Dotenv()
	env := make([]string, 0, len(values))
	for key, value := range values {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}

	return env
}

func (e *mappedScopedEnvironment) GetSubscriptionId() string {
	return e.Getenv(SubscriptionIdEnvVarName)
}

func (e *mappedScopedEnvironment) SetSubscriptionId(id string) {
	e.DotenvSet(SubscriptionIdEnvVarName, id)
}

func (e *mappedScopedEnvironment) GetTenantId() string {
	return e.Getenv(TenantIdEnvVarName)
}

func (e *mappedScopedEnvironment) GetLocation() string {
	return e.Getenv(LocationEnvVarName)
}

func (e *mappedScopedEnvironment) SetLocation(location string) {
	e.DotenvSet(LocationEnvVarName, location)
}

func (e *mappedScopedEnvironment) GetServiceProperty(serviceName, propertyName string) string {
	return e.Getenv(fmt.Sprintf("SERVICE_%s_%s", Key(serviceName), propertyName))
}

func (e *mappedScopedEnvironment) SetServiceProperty(serviceName, propertyName, value string) {
	e.DotenvSet(fmt.Sprintf("SERVICE_%s_%s", Key(serviceName), propertyName), value)
}

func (e *mappedScopedEnvironment) projectKey(key string) string {
	if mapped, ok := e.aliases[key]; ok {
		return mapped
	}

	return key
}

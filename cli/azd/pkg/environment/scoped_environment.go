// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"fmt"
	"maps"
	"runtime"
	"slices"
	"strings"

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

	// Name returns the backing environment name, falling back to AZURE_ENV_NAME when it is empty.
	Name() string
	// Getenv returns a provider-scoped value, preferring dotenv over the process environment.
	Getenv(key string) string
	// LookupEnv is like Getenv, but distinguishes an unset value from an explicitly empty value.
	LookupEnv(key string) (string, bool)
	// Dotenv returns a copy of the provider-visible dotenv values without process environment fallbacks.
	Dotenv() map[string]string
	// DotenvSet writes a provider-scoped value to the backing dotenv state; Save persists the change.
	DotenvSet(key, value string)
	// DotenvDelete removes a provider-scoped value from backing dotenv state; process fallback may remain.
	DotenvDelete(key string)
	// Environ formats only the provider-visible dotenv values as KEY=VALUE entries.
	Environ() []string
	// ProcessEnviron builds a complete environment for a child process.
	// base is typically os.Environ(), which supplies the real process environment.
	//
	//	childEnv := env.ProcessEnviron(os.Environ())
	//	runArgs := exec.NewRunArgs("tool").WithExactEnv(childEnv)
	ProcessEnviron(base []string) []string
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

// ProcessEnviron returns inherited values followed by dotenv values, which take precedence.
func (e *Environment) ProcessEnviron(base []string) []string {
	dotenv := e.Environ()
	values := make([]string, 0, len(base)+len(dotenv))
	values = append(values, base...)
	return append(values, dotenv...)
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
		if value, found := values[sharedName]; found {
			mapped[localName] = value
		}
	}
	for localName := range e.aliases {
		delete(values, localName)
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

// ProcessEnviron applies aliases to the complete underlying environment.
// Missing sources remove their provider names; explicitly empty sources remain present.
func (e *mappedScopedEnvironment) ProcessEnviron(base []string) []string {
	resolvedBase := e.ScopedEnvironment.ProcessEnviron(base)
	providerNames := slices.Sorted(maps.Keys(e.aliases))
	values := slices.DeleteFunc(slices.Clone(resolvedBase), func(entry string) bool {
		name, _, found := strings.Cut(entry, "=")
		return found && slices.ContainsFunc(providerNames, func(providerName string) bool {
			return processEnvNameEqual(name, providerName)
		})
	})

	// Resolve sources from one snapshot so overlapping aliases apply only once.
	for _, providerName := range providerNames {
		if isLoaderControlKey(providerName) {
			continue
		}
		if value, found := lookupProcessEnv(resolvedBase, e.aliases[providerName]); found {
			values = append(values, providerName+"="+value)
		}
	}
	return values
}

func lookupProcessEnv(values []string, key string) (string, bool) {
	for _, entry := range slices.Backward(values) {
		name, value, found := strings.Cut(entry, "=")
		if found && processEnvNameEqual(name, key) {
			return value, true
		}
	}
	return "", false
}

func processEnvNameEqual(name, key string) bool {
	// Windows treats process environment names as case-insensitive, unlike Unix.
	if runtime.GOOS == "windows" {
		return strings.EqualFold(name, key)
	}
	return name == key
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

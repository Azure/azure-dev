// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"fmt"

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
	inputs  map[string]string
	outputs map[string]string
}

// NewMappedScopedEnvironment creates a provider-facing environment that maps layer-local input
// and output names to names in the shared environment.
func NewMappedScopedEnvironment(
	env ScopedEnvironment,
	inputs map[string]string,
	outputs map[string]string,
) ScopedEnvironment {
	if len(inputs) == 0 && len(outputs) == 0 {
		return env
	}

	return &mappedScopedEnvironment{
		ScopedEnvironment: env,
		inputs:            inputs,
		outputs:           outputs,
	}
}

func (e *mappedScopedEnvironment) Getenv(key string) string {
	return e.ScopedEnvironment.Getenv(e.inputKey(key))
}

func (e *mappedScopedEnvironment) LookupEnv(key string) (string, bool) {
	return e.ScopedEnvironment.LookupEnv(e.inputKey(key))
}

func (e *mappedScopedEnvironment) Dotenv() map[string]string {
	values := e.ScopedEnvironment.Dotenv()

	// Resolve every source before adding local names so overlapping aliases do not affect each other.
	mapped := make(map[string]string, len(e.inputs))
	for localName, sharedName := range e.inputs {
		mapped[localName], _ = e.ScopedEnvironment.LookupEnv(sharedName)
	}
	for localName, value := range mapped {
		values[localName] = value
	}

	return values
}

func (e *mappedScopedEnvironment) DotenvSet(key string, value string) {
	e.ScopedEnvironment.DotenvSet(e.outputKey(key), value)
}

func (e *mappedScopedEnvironment) DotenvDelete(key string) {
	e.ScopedEnvironment.DotenvDelete(e.outputKey(key))
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

func (e *mappedScopedEnvironment) inputKey(key string) string {
	if mapped, ok := e.inputs[key]; ok {
		return mapped
	}

	return key
}

func (e *mappedScopedEnvironment) outputKey(key string) string {
	if mapped, ok := e.outputs[key]; ok {
		return mapped
	}

	return key
}

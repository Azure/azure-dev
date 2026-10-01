// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"fmt"
	"maps"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

// ProviderScopedEnv lets you make it so input and output aliasing is applied when getting or setting values,
// either via Dotenv, or through the environment.
type ProviderScopedEnv struct {
	realEnv       ProviderEnv
	inputAliases  map[string]string
	outputAliases map[string]string
}

var _ ProviderEnv = (*ProviderScopedEnv)(nil)

// NewProviderScopedEnv creates a provider view.
//   - realEnv - the underlying store, which retains the actual environment changes. Returned by
//     [ProviderScopedEnv.PersistableEnv].
//   - inputAliases - map any read request for the 'key' variable to instead use the 'value' variable.
//   - outputAliases - map any write request for 'key' variable to instead use the 'value' variable.
//
// Unmapped keys pass through unchanged.
func NewProviderScopedEnv(realEnv ProviderEnv, inputAliases, outputAliases map[string]string) *ProviderScopedEnv {
	return &ProviderScopedEnv{
		realEnv:       realEnv,
		inputAliases:  maps.Clone(inputAliases),
		outputAliases: maps.Clone(outputAliases),
	}
}

// PersistableEnv returns the underlying environment without provider mappings.
func (e *ProviderScopedEnv) PersistableEnv() *Environment {
	return e.realEnv.PersistableEnv()
}

func (e *ProviderScopedEnv) Getenv(key string) string {
	if source, ok := e.inputAliases[key]; ok {
		key = source
	}
	return e.realEnv.Getenv(key)
}

func (e *ProviderScopedEnv) LookupEnv(key string) (string, bool) {
	if source, ok := e.inputAliases[key]; ok {
		key = source
	}
	return e.realEnv.LookupEnv(key)
}

func (e *ProviderScopedEnv) Environ() []string {
	dotenv := e.Dotenv()
	var envVars []string
	for k, v := range dotenv {
		envVars = append(envVars, fmt.Sprintf("%s=%s", k, v))
	}
	return envVars
}

func (e *ProviderScopedEnv) DotenvDelete(key string) {
	if target, ok := e.outputAliases[key]; ok {
		key = target
	}
	e.realEnv.DotenvDelete(key)
}

func (e *ProviderScopedEnv) Dotenv() map[string]string {
	values := e.realEnv.Dotenv()
	snapshot := maps.Clone(values)

	// Resolve overlapping aliases from the original values, never from a rewritten key.
	for target, source := range e.inputAliases {
		delete(values, source)
		delete(values, target)
	}
	for target, source := range e.inputAliases {
		if value, ok := snapshot[source]; ok {
			values[target] = value
		}
	}
	return values
}

func (e *ProviderScopedEnv) DotenvSet(key, value string) {
	if target, ok := e.outputAliases[key]; ok {
		key = target
	}
	e.realEnv.DotenvSet(key, value)
}

func (e *ProviderScopedEnv) Name() string {
	return e.realEnv.Name()
}

func (e *ProviderScopedEnv) GetConfig() config.Config {
	return e.realEnv.GetConfig()
}

func (e *ProviderScopedEnv) GetSubscriptionId() string {
	return e.Getenv(SubscriptionIdEnvVarName)
}

func (e *ProviderScopedEnv) SetSubscriptionId(id string) {
	e.DotenvSet(SubscriptionIdEnvVarName, id)
}

func (e *ProviderScopedEnv) GetTenantId() string {
	return e.Getenv(TenantIdEnvVarName)
}

func (e *ProviderScopedEnv) GetLocation() string {
	return e.Getenv(LocationEnvVarName)
}

func (e *ProviderScopedEnv) SetLocation(location string) {
	e.DotenvSet(LocationEnvVarName, location)
}

func (e *ProviderScopedEnv) GetServiceProperty(serviceName, propertyName string) string {
	return e.Getenv(fmt.Sprintf("SERVICE_%s_%s", Key(serviceName), propertyName))
}

func (e *ProviderScopedEnv) SetServiceProperty(serviceName, propertyName, value string) {
	e.DotenvSet(fmt.Sprintf("SERVICE_%s_%s", Key(serviceName), propertyName), value)
}

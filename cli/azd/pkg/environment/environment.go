// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"maps"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/joho/godotenv"
)

// EnvNameEnvVarName is the name of the key used to store the envname property in the environment.
const EnvNameEnvVarName = "AZURE_ENV_NAME"

// LocationEnvVarName is the name of the key used to store the location property in the environment.
const LocationEnvVarName = "AZURE_LOCATION"

// SubscriptionIdEnvVarName is the name of they key used to store the subscription id property in the environment.
const SubscriptionIdEnvVarName = "AZURE_SUBSCRIPTION_ID"

// PrincipalIdEnvVarName is the name of they key used to store the id of a principal in the environment.
const PrincipalIdEnvVarName = "AZURE_PRINCIPAL_ID"

// TenantIdEnvVarName is the tenant that owns the subscription
const TenantIdEnvVarName = "AZURE_TENANT_ID"

// ContainerRegistryEndpointEnvVarName is the name of they key used to store the endpoint of the container registry to push
// to.
const ContainerRegistryEndpointEnvVarName = "AZURE_CONTAINER_REGISTRY_ENDPOINT"

// AksClusterEnvVarName is the name of they key used to store the endpoint of the AKS cluster to push to.
const AksClusterEnvVarName = "AZURE_AKS_CLUSTER_NAME"

// ResourceGroupEnvVarName is the name of the azure resource group that should be used for deployments
const ResourceGroupEnvVarName = "AZURE_RESOURCE_GROUP"

// PlatformTypeEnvVarName is the name of the key used to store the current azd platform type
const PlatformTypeEnvVarName = "AZD_PLATFORM_TYPE"

// The zero value of an Environment is not valid. Use [New] to create one. When writing tests,
// [Ephemeral] and [EphemeralWithValues] are useful to create environments which are not persisted to disk.
type Environment struct {
	name string

	// dotenv is a map of keys to values, persisted to the `.env` file stored in this environment's [Root].
	dotenv map[string]string

	// deletedKeys keeps track of deleted keys from the `.env` to be reapplied before a merge operation
	// happens in Save
	deletedKeys map[string]struct{}

	// Config is environment specific config
	Config config.Config
}

const AzdInitialEnvironmentConfigName = "AZD_INITIAL_ENVIRONMENT_CONFIG"

// New returns a new environment with the specified name.
func New(name string) *Environment {
	env := &Environment{
		name:        name,
		dotenv:      make(map[string]string),
		deletedKeys: make(map[string]struct{}),
		Config:      getInitialConfig(),
	}

	env.DotenvSet(EnvNameEnvVarName, name)

	return env
}

func getInitialConfig() config.Config {
	initialConfig := os.Getenv(AzdInitialEnvironmentConfigName)
	if initialConfig == "" {
		return config.NewEmptyConfig()
	}

	var initConfig map[string]any
	if err := json.Unmarshal([]byte(initialConfig), &initConfig); err != nil {
		log.Println("Failed to unmarshal initial config", err, "Using empty config.")
		return config.NewEmptyConfig()
	}

	return config.NewConfig(initConfig)
}

// NewWithValues returns an ephemeral environment (i.e. not backed by a data store) with a set
// of values. Useful for testing. The name parameter is added to the environment with the
// AZURE_ENV_NAME key, replacing an existing value in the provided values map. A nil values is
// treated the same way as an empty map.
func NewWithValues(name string, values map[string]string) *Environment {
	env := New(name)

	if values != nil {
		env.dotenv = values
	}

	return env
}

type EnvironmentResolver func(ctx context.Context) (*Environment, error)

// Same restrictions as a deployment name (ref:
// https://docs.microsoft.com/azure/azure-resource-manager/management/resource-name-rules#microsoftresources)
var EnvironmentNameRegexp = regexp.MustCompile(`^[a-zA-Z0-9-\(\)_\.]{1,64}$`)

// The maximum length of an environment name.
var EnvironmentNameMaxLength = 64

func IsValidEnvironmentName(name string) bool {
	return EnvironmentNameRegexp.MatchString(name)
}

// CleanName returns a version of [name] where all characters not allowed in an environment name have been replaced
// with hyphens
func CleanName(name string) string {
	result := strings.Builder{}

	for _, c := range name {
		if (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '-' ||
			c == '(' ||
			c == ')' ||
			c == '_' ||
			c == '.' {
			result.WriteRune(c)
		} else {
			result.WriteRune('-')
		}
	}

	return result.String()
}

// Getenv behaves like os.Getenv, except that any keys in the `.env` file associated with this environment are considered
// first.
func (e *Environment) Getenv(key string) string {
	if v, has := e.dotenv[key]; has {
		return v
	}

	return os.Getenv(key)
}

// LookupEnv behaves like os.LookupEnv, except that any keys in the `.env` file associated with this environment are
// considered first.
func (e *Environment) LookupEnv(key string) (string, bool) {
	if v, has := e.dotenv[key]; has {
		return v, true
	}

	return os.LookupEnv(key)
}

// DotenvDelete removes the given key from the .env file in the environment, it is a no-op if the key
// does not exist. [Save] should be called to ensure this change is persisted.
func (e *Environment) DotenvDelete(key string) {
	delete(e.dotenv, key)
	e.deletedKeys[key] = struct{}{}
}

// Dotenv returns a copy of the key value pairs from the .env file in the environment.
func (e *Environment) Dotenv() map[string]string {
	return maps.Clone(e.dotenv)
}

// DotenvSet sets the value of [key] to [value] in the .env file associated with the environment. [Save] should be
// called to ensure this change is persisted.
func (e *Environment) DotenvSet(key string, value string) {
	e.dotenv[key] = value
	delete(e.deletedKeys, key)
}

// Name gets the name of the environment
// If empty will fallback to the value of the AZURE_ENV_NAME environment variable
func (e *Environment) Name() string {
	if e.name == "" {
		e.name = e.Getenv(EnvNameEnvVarName)
	}

	return e.name
}

// GetSubscriptionId is shorthand for Getenv(SubscriptionIdEnvVarName)
func (e *Environment) GetSubscriptionId() string {
	return e.Getenv(SubscriptionIdEnvVarName)
}

// GetTenantId is shorthand for Getenv(TenantIdEnvVarName)
func (e *Environment) GetTenantId() string {
	return e.Getenv(TenantIdEnvVarName)
}

// SetLocation is shorthand for DotenvSet(SubscriptionIdEnvVarName, location)
func (e *Environment) SetSubscriptionId(id string) {
	e.DotenvSet(SubscriptionIdEnvVarName, id)
}

// GetLocation is shorthand for Getenv(LocationEnvVarName)
func (e *Environment) GetLocation() string {
	return e.Getenv(LocationEnvVarName)
}

// SetLocation is shorthand for DotenvSet(LocationEnvVarName, location)
func (e *Environment) SetLocation(location string) {
	e.DotenvSet(LocationEnvVarName, location)
}

// Key returns the environment key name for the given name.
func Key(name string) string {
	return strings.ReplaceAll(strings.ToUpper(name), "-", "_")
}

// GetServiceProperty is shorthand for Getenv(SERVICE_$SERVICE_NAME_$PROPERTY_NAME)
func (e *Environment) GetServiceProperty(serviceName string, propertyName string) string {
	return e.Getenv(fmt.Sprintf("SERVICE_%s_%s", Key(serviceName), propertyName))
}

// Sets the value of a service-namespaced property in the environment.
func (e *Environment) SetServiceProperty(serviceName string, propertyName string, value string) {
	e.DotenvSet(fmt.Sprintf("SERVICE_%s_%s", Key(serviceName), propertyName), value)
}

// Creates a slice of key value pairs, based on the entries in the `.env` file like `KEY=VALUE` that
// can be used to pass into command runner or similar constructs.
func (e *Environment) Environ() []string {
	envVars := []string{}
	for k, v := range e.dotenv {
		envVars = append(envVars, fmt.Sprintf("%s=%s", k, v))
	}

	return envVars
}

// fixupUnquotedDotenv is a workaround for behavior in how godotenv.Marshal handles numeric like values.  Marshaling
// a map[string]string to a dotenv file, if a value can be successfully parsed with strconv.Atoi, it will be written in
// the dotenv file without quotes and the value written will be the value returned by strconv.Atoi. This can lead to dropping
// leading zeros from the value that we persist.
//
// For example, given a map with the key value pair ("FOO", "01"), the value returned by godotenv.Marshal will have a line
// that looks like FOO=1 instead of FOO=01 or FOO="01".
//
// This function takes the value returned by godotenv.Marshal and for any unquoted value replaces it with the value from
// the values map if they differ.  This means that a key value pair ("FOO", "1") remains as FOO=1.
//
// When replacing a key in this manner, we ensure the value is wrapped in quotes, on the assumption that the leading zero
// is of significance to the value and wrapping it quotes means it is more likely to be treated as a string instead of a
// number by any downstream systems. We do not need to worry about escaping quotes in the value, because we know that
// godotenv.Marshal only did this translation for numeric values and so we know the original value did not contain quotes.
func fixupUnquotedDotenv(values map[string]string, dotenv string) string {
	entries := strings.Split(dotenv, "\n")
	for idx, line := range entries {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		envKey := parts[0]
		envValue := parts[1]

		if len(envValue) > 0 && envValue[0] != '"' {
			if values[envKey] != envValue {
				entries[idx] = fmt.Sprintf("%s=\"%s\"", envKey, values[envKey])
			}
		}
	}

	return strings.Join(entries, "\n")
}

// Prepare dotenv for saving and returns a marshalled string that can be save to the underlying data store
// Instead of calling `godotenv.Write` directly, we need to save the file ourselves, so we can fixup any numeric values
// that were incorrectly unquoted.
func marshallDotEnv(env *Environment) (string, error) {
	marshalled, err := godotenv.Marshal(env.dotenv)
	if err != nil {
		return "", fmt.Errorf("marshalling .env: %w", err)
	}

	return fixupUnquotedDotenv(env.dotenv, marshalled), nil
}

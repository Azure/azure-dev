// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
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

// ContainerRegistryEndpointEnvVarName is the name of they key used to store the endpoint of the container registry to push to.
const ContainerRegistryEndpointEnvVarName = "AZURE_CONTAINER_REGISTRY_ENDPOINT"

// ResourceGroupEnvVarName is the name of the azure resource group that should be used for deployments
const ResourceGroupEnvVarName = "AZURE_RESOURCE_GROUP"

type Environment struct {
	// values is a map of setting names to values.
	// all keys are converted to upper case before it is set.
	values map[string]string
	// File is a path to the file that backs this environment. If empty, the Environment
	// will not be persisted when `Save` is called. This allows the zero value to be used
	// for testing.
	File string
}

// DeleteVariable removes an entry from azd environment which key matches the variable name.
func (e *Environment) DeleteVariable(variableName string) {
	delete(e.values, strings.ToUpper(variableName))
}

// SetVariable update or create an entry to the azd environment.
// key is converted to upper case.
func (e *Environment) SetVariable(key, value string) {
	e.values[strings.ToUpper(key)] = value
}

// GetValue get the value of an entry with the requested key.
// return empty string and false if the key is not found.
// Use ValueOf if you don't need to check if value is found
func (e *Environment) GetValue(key string) (string, bool) {
	value, found := e.values[strings.ToUpper(key)]
	return value, found
}

// GetValue get the value of an entry with the requested key.
// return empty string and false if the key is not found.
// Use GetValue to return if the value is found as well
func (e *Environment) ValueOf(key string) string {
	return e.values[strings.ToUpper(key)]
}

// HasValue return true if the key is found and the value is not empty string.
func (e *Environment) HasValue(key string) bool {
	value, found := e.values[strings.ToUpper(key)]
	return found && value != ""
}

// Init make sure that values has an empty map.
func (e *Environment) Init() {
	if e.values == nil {
		e.values = make(map[string]string)
	}
}

// CopyValues get a copy of the azd environment variable entries.
func (e *Environment) CopyValues() map[string]string {
	if e.values == nil {
		return nil
	}

	result := make(map[string]string)
	for k, v := range e.values {
		// keys are already upper case
		result[k] = v
	}
	return result
}

// ToStringArray create an array of strings where each string is in the form of
// "key=value"
func (e *Environment) ToStringArray() []string {
	result := make([]string, 0, len(e.values)+1)
	for k, v := range e.values {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	return result
}

// Same restrictions as a deployment name (ref: https://docs.microsoft.com/azure/azure-resource-manager/management/resource-name-rules#microsoftresources)
var environmentNameRegexp = regexp.MustCompile(`^[a-zA-Z0-9-\(\)_\.]{1,64}$`)

func IsValidEnvironmentName(name string) bool {
	return environmentNameRegexp.MatchString(name)
}

// FromFile loads an environment from a file on disk. On error,
// an valid empty environment file, configured to persist its contents
// to file, is returned.
func FromFile(file string) (Environment, error) {
	env := Environment{
		values: make(map[string]string),
		File:   file,
	}

	e, err := godotenv.Read(file)
	if err != nil {
		env.values = make(map[string]string)
		return env, fmt.Errorf("can't read %s: %w", file, err)
	}

	env.values = e
	return env, nil
}

func GetEnvironment(azdContext *azdcontext.AzdContext, name string) (Environment, error) {
	return FromFile(azdContext.GetEnvironmentFilePath(name))
}

// Empty returns an empty environment, which will be persisted
// to a given file when saved.
func Empty(file string) Environment {
	return Environment{
		File:   file,
		values: make(map[string]string),
	}
}

// If `File` is set, Save writes the current contents of the environment to
// the given file, creating it and any intermediate directories as needed.
func (e *Environment) Save() error {
	if e.File == "" {
		return nil
	}

	err := os.MkdirAll(filepath.Dir(e.File), osutil.PermissionDirectory)
	if err != nil {
		return fmt.Errorf("failed to create a directory: %w", err)
	}

	err = godotenv.Write(e.values, e.File)
	if err != nil {
		return fmt.Errorf("can't write '%s': %w", e.File, err)
	}

	return nil
}

func (e *Environment) GetEnvName() string {
	return e.values[EnvNameEnvVarName]
}

func (e *Environment) SetEnvName(envname string) {
	e.values[EnvNameEnvVarName] = envname
}

func (e *Environment) GetSubscriptionId() string {
	return e.values[SubscriptionIdEnvVarName]
}

func (e *Environment) GetTenantId() string {
	return e.values[TenantIdEnvVarName]
}

func (e *Environment) SetSubscriptionId(id string) {
	e.values[SubscriptionIdEnvVarName] = id
}

func (e *Environment) GetLocation() string {
	return e.values[LocationEnvVarName]
}

func (e *Environment) SetLocation(location string) {
	e.values[LocationEnvVarName] = location
}

func (e *Environment) SetPrincipalId(principalID string) {
	e.values[PrincipalIdEnvVarName] = principalID
}

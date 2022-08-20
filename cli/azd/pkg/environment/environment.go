// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

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
	// Values is a map of setting names to values.
	Values map[string]string
	// File is a path to the file that backs this environment. If empty, the Environment
	// will not be persisted when `Save` is called. This allows the zero value to be used
	// for testing.
	File string
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
		Values: make(map[string]string),
		File:   file,
	}

	e, err := godotenv.Read(file)
	if err != nil {
		env.Values = make(map[string]string)
		return env, fmt.Errorf("can't read %s: %w", file, err)
	}

	env.Values = e
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
		Values: make(map[string]string),
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

	err = godotenv.Write(e.Values, e.File)
	if err != nil {
		return fmt.Errorf("can't write '%s': %w", e.File, err)
	}

	return nil
}

func (e *Environment) GetEnvName() string {
	return e.Values[EnvNameEnvVarName]
}

func (e *Environment) SetEnvName(envname string) {
	e.Values[EnvNameEnvVarName] = envname
}

func (e *Environment) GetSubscriptionId() string {
	return e.Values[SubscriptionIdEnvVarName]
}

func (e *Environment) GetTenantId() string {
	return e.Values[TenantIdEnvVarName]
}

func (e *Environment) SetSubscriptionId(id string) {
	e.Values[SubscriptionIdEnvVarName] = id
}

func (e *Environment) SetLocation(location string) {
	e.Values[LocationEnvVarName] = location
}

func (e *Environment) SetPrincipalId(principalID string) {
	e.Values[PrincipalIdEnvVarName] = principalID
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
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

// ContainerRegistryEndpointEnvVarName is the name of they key used to store the endpoint of the container registry to push
// to.
const ContainerRegistryEndpointEnvVarName = "AZURE_CONTAINER_REGISTRY_ENDPOINT"

// ResourceGroupEnvVarName is the name of the azure resource group that should be used for deployments
const ResourceGroupEnvVarName = "AZURE_RESOURCE_GROUP"

type Environment struct {
	// Values is a map of setting names to values.
	Values map[string]string

	// Config is environment specific config
	Config config.Config

	// File is a path to the directory that backs this environment. If empty, the Environment
	// will not be persisted when `Save` is called. This allows the zero value to be used
	// for testing.
	Root string
}

// Same restrictions as a deployment name (ref:
// https://docs.microsoft.com/azure/azure-resource-manager/management/resource-name-rules#microsoftresources)
var environmentNameRegexp = regexp.MustCompile(`^[a-zA-Z0-9-\(\)_\.]{1,64}$`)

func IsValidEnvironmentName(name string) bool {
	return environmentNameRegexp.MatchString(name)
}

// FromRoot loads an environment located in a directory. On error,
// an valid empty environment file, configured to persist its contents
// to this directory, is returned.
func FromRoot(root string) (*Environment, error) {
	if _, err := os.Stat(root); err != nil {
		return EmptyWithRoot(root), err
	}

	env := &Environment{
		Root: root,
	}

	envPath := filepath.Join(root, azdcontext.DotEnvFileName)
	if e, err := godotenv.Read(envPath); errors.Is(err, os.ErrNotExist) {
		env.Values = make(map[string]string)
	} else if err != nil {
		return EmptyWithRoot(root), fmt.Errorf("loading .env: %w", err)
	} else {
		env.Values = e
	}

	cfgPath := filepath.Join(root, azdcontext.ConfigFileName)

	cfgMgr := config.NewManager()
	if cfg, err := cfgMgr.Load(cfgPath); errors.Is(err, os.ErrNotExist) {
		env.Config = config.NewConfig(nil)
	} else if err != nil {
		return EmptyWithRoot(root), fmt.Errorf("loading config: %w", err)
	} else {
		env.Config = cfg
	}

	return env, nil
}

func GetEnvironment(azdContext *azdcontext.AzdContext, name string) (*Environment, error) {
	return FromRoot(azdContext.EnvironmentRoot(name))
}

// EmptyWithRoot returns an empty environment, which will be persisted
// to a given directory when saved.
func EmptyWithRoot(root string) *Environment {
	return &Environment{
		Root:   root,
		Values: make(map[string]string),
		Config: config.NewConfig(nil),
	}
}

func Ephemeral() *Environment {
	return &Environment{
		Values: make(map[string]string),
		Config: config.NewConfig(nil),
	}
}

// EphemeralWithValues returns an ephemeral environment (i.e. not backed by a file) with a set
// of values. Useful for testing. The name parameter is added to the environment with the
// AZURE_ENV_NAME key, replacing an existing value in the provided values map. A nil values is
// treated the same way as an empty map.
func EphemeralWithValues(name string, values map[string]string) *Environment {
	env := Ephemeral()

	if values != nil {
		env.Values = values
	}

	env.Values[EnvNameEnvVarName] = name

	return env
}

// Getenv fetches a key from e.Values, falling back to os.Getenv if it is not present.
func (e *Environment) Getenv(key string) string {
	if v, has := e.Values[key]; has {
		return v
	}

	return os.Getenv(key)
}

// If `Root` is set, Save writes the current contents of the environment to
// the given directory, creating it and any intermediate directories as needed.
func (e *Environment) Save() error {
	if e.Root == "" {
		return nil
	}

	err := os.MkdirAll(e.Root, osutil.PermissionDirectory)
	if err != nil {
		return fmt.Errorf("failed to create a directory: %w", err)
	}

	err = godotenv.Write(e.Values, filepath.Join(e.Root, azdcontext.DotEnvFileName))
	if err != nil {
		return fmt.Errorf("saving .env: %w", err)
	}

	cfgMgr := config.NewManager()

	err = cfgMgr.Save(e.Config, filepath.Join(e.Root, azdcontext.ConfigFileName))
	if err != nil {
		return fmt.Errorf("saving config: %w", err)
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

func (e *Environment) GetLocation() string {
	return e.Values[LocationEnvVarName]
}

func (e *Environment) SetLocation(location string) {
	e.Values[LocationEnvVarName] = location
}

func (e *Environment) SetPrincipalId(principalID string) {
	e.Values[PrincipalIdEnvVarName] = principalID
}

func (e *Environment) GetPrincipalId() string {
	return e.Values[PrincipalIdEnvVarName]
}

func normalize(key string) string {
	return strings.ReplaceAll(strings.ToUpper(key), "-", "_")
}

// Returns the value of a service-namespaced property in the environment.
func (e *Environment) GetServiceProperty(serviceName string, propertyName string) string {
	return e.Values[fmt.Sprintf("SERVICE_%s_%s", normalize(serviceName), propertyName)]
}

// Sets the value of a service-namespaced property in the environment.
func (e *Environment) SetServiceProperty(serviceName string, propertyName string, value string) {
	e.Values[fmt.Sprintf("SERVICE_%s_%s", normalize(serviceName), propertyName)] = value
}

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

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/joho/godotenv"
	"golang.org/x/exp/maps"
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

// The zero value of an Environment is not valid. Use [FromRoot] or [EmptyWithRoot] to create one. When writing tests,
// [Ephemeral] and [EphemeralWithValues] are useful to create environments which are not persisted to disk.
type Environment struct {
	// dotenv is a map of keys to values, persisted to the `.env` file stored in this environment's [Root].
	dotenv map[string]string

	dotEnvDeleted map[string]bool

	// Config is environment specific config
	Config config.Config

	// File is a path to the directory that backs this environment. If empty, the Environment
	// will not be persisted when `Save` is called. This allows the zero value to be used
	// for testing.
	Root string
}

type EnvironmentResolver func() (*Environment, error)

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

	if err := env.Reload(); err != nil {
		return EmptyWithRoot(root), err
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
		dotenv: make(map[string]string),
		Config: config.NewEmptyConfig(),
	}
}

// Ephemeral returns returns an empty ephemeral environment (i.e. not backed by a file) with a set
func Ephemeral() *Environment {
	return &Environment{
		dotenv: make(map[string]string),
		Config: config.NewEmptyConfig(),
	}
}

// EphemeralWithValues returns an ephemeral environment (i.e. not backed by a file) with a set
// of values. Useful for testing. The name parameter is added to the environment with the
// AZURE_ENV_NAME key, replacing an existing value in the provided values map. A nil values is
// treated the same way as an empty map.
func EphemeralWithValues(name string, values map[string]string) *Environment {
	env := Ephemeral()

	if values != nil {
		env.dotenv = values
	}

	env.dotenv[EnvNameEnvVarName] = name

	return env
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
	e.dotEnvDeleted[key] = true
}

// Dotenv returns a copy of the key value pairs from the .env file in the environment.
func (e *Environment) Dotenv() map[string]string {
	return maps.Clone(e.dotenv)
}

// DotenvSet sets the value of [key] to [value] in the .env file associated with the environment. [Save] should be
// called to ensure this change is persisted.
func (e *Environment) DotenvSet(key string, value string) {
	e.dotenv[key] = value
}

// Reloads environment variables and configuration
func (e *Environment) Reload() error {
	// Reload env values
	envPath := filepath.Join(e.Root, azdcontext.DotEnvFileName)
	if envMap, err := godotenv.Read(envPath); errors.Is(err, os.ErrNotExist) {
		e.dotenv = make(map[string]string)
		e.dotEnvDeleted = make(map[string]bool)
	} else if err != nil {
		return fmt.Errorf("loading .env: %w", err)
	} else {
		e.dotenv = envMap
		e.dotEnvDeleted = make(map[string]bool)
	}

	// Reload env config
	cfgPath := filepath.Join(e.Root, azdcontext.ConfigFileName)
	cfgMgr := config.NewManager()
	if cfg, err := cfgMgr.Load(cfgPath); errors.Is(err, os.ErrNotExist) {
		e.Config = config.NewEmptyConfig()
	} else if err != nil {
		return fmt.Errorf("loading config: %w", err)
	} else {
		e.Config = cfg
	}

	if e.GetEnvName() != "" {
		tracing.SetUsageAttributes(fields.StringHashed(fields.EnvNameKey, e.GetEnvName()))
	}

	if e.GetSubscriptionId() != "" {
		tracing.SetGlobalAttributes(fields.SubscriptionIdKey.String(e.GetSubscriptionId()))
	}

	return nil
}

// If `Root` is set, Save writes the current contents of the environment to
// the given directory, creating it and any intermediate directories as needed.
func (e *Environment) Save() error {
	if e.Root == "" {
		return nil
	}

	// Update configuration
	cfgMgr := config.NewManager()
	if err := cfgMgr.Save(e.Config, filepath.Join(e.Root, azdcontext.ConfigFileName)); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	// Cache current values & reload to get any new env vars
	currentValues := e.dotenv
	deletedValues := e.dotEnvDeleted
	if err := e.Reload(); err != nil {
		return fmt.Errorf("failed reloading env vars, %w", err)
	}

	// Overlay current values before saving
	for key, value := range currentValues {
		e.dotenv[key] = value
	}

	// Replay deletion
	for key := range deletedValues {
		delete(e.dotenv, key)
	}

	err := os.MkdirAll(e.Root, osutil.PermissionDirectory)
	if err != nil {
		return fmt.Errorf("failed to create a directory: %w", err)
	}

	err = godotenv.Write(e.dotenv, filepath.Join(e.Root, azdcontext.DotEnvFileName))
	if err != nil {
		return fmt.Errorf("saving .env: %w", err)
	}

	tracing.SetUsageAttributes(fields.StringHashed(fields.EnvNameKey, e.GetEnvName()))
	return nil
}

// GetEnvName is shorthand for Getenv(EnvNameEnvVarName)
func (e *Environment) GetEnvName() string {
	return e.Getenv(EnvNameEnvVarName)
}

// SetEnvName is shorthand for DotenvSet(EnvNameEnvVarName, envname)
func (e *Environment) SetEnvName(envname string) {
	e.dotenv[EnvNameEnvVarName] = envname
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
	e.dotenv[SubscriptionIdEnvVarName] = id
}

// GetLocation is shorthand for Getenv(LocationEnvVarName)
func (e *Environment) GetLocation() string {
	return e.Getenv(LocationEnvVarName)
}

// SetLocation is shorthand for DotenvSet(LocationEnvVarName, location)
func (e *Environment) SetLocation(location string) {
	e.dotenv[LocationEnvVarName] = location
}

func normalize(key string) string {
	return strings.ReplaceAll(strings.ToUpper(key), "-", "_")
}

// GetServiceProperty is shorthand for Getenv(SERVICE_$SERVICE_NAME_$PROPERTY_NAME)
func (e *Environment) GetServiceProperty(serviceName string, propertyName string) string {
	return e.Getenv(fmt.Sprintf("SERVICE_%s_%s", normalize(serviceName), propertyName))
}

// Sets the value of a service-namespaced property in the environment.
func (e *Environment) SetServiceProperty(serviceName string, propertyName string, value string) {
	e.dotenv[fmt.Sprintf("SERVICE_%s_%s", normalize(serviceName), propertyName)] = value
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

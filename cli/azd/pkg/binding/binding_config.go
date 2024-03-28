package binding

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicelinker/armservicelinker/v2"
)

// A child property of `services` in azure.yaml, used to define bindings of a service
type BindingConfig struct {
	Name           string             `yaml:"name"`
	TargetType     TargetResourceType `yaml:"targetType"`
	TargetResource string             `yaml:"targetResource"`
	StoreType      StoreResourceType  `yaml:"StoreResourceType"`
	StoreResource  string             `yaml:"storeResource"`
}

// Describe a binding source service
type BindingSource struct {
	SourceType     SourceResourceType
	SourceResource string
	ClientType     armservicelinker.ClientType
}

// Binding supported source resource types
type SourceResourceType string

const (
	SourceTypeWebApp       SourceResourceType = "appservice"
	SourceTypeFunctionApp  SourceResourceType = "functionapp"
	SourceTypeContainerApp SourceResourceType = "containerapp"
	SourceTypeSpringApp    SourceResourceType = "springapp"
)

func (sourceType SourceResourceType) IsValid() bool {
	switch sourceType {
	case SourceTypeWebApp,
		SourceTypeFunctionApp,
		SourceTypeContainerApp,
		SourceTypeSpringApp:
		return true
	}
	return false
}

// Binding supported target resource types
type TargetResourceType string

const (
	TargetTypeStorageAccount     TargetResourceType = "storage"
	TargetTypeCosmosDB           TargetResourceType = "cosmos"
	TargetTypePostgreSqlFlexible TargetResourceType = "postgresql-flexible"
	TargetTypeMysqlFlexible      TargetResourceType = "mysql-flexible"
	TargetTypeSql                TargetResourceType = "sql"
	TargetTypeRedis              TargetResourceType = "redis"
	TargetTypeRedisEnterprise    TargetResourceType = "redis-enterprise"
	TargetTypeKeyVault           TargetResourceType = "keyvault"
	TargetTypeEventHub           TargetResourceType = "eventhub"
	TargetTypeAppConfig          TargetResourceType = "appconfig"
	TargetTypeServiceBus         TargetResourceType = "servicebus"
	TargetTypeSignalR            TargetResourceType = "signalr"
	TargetTypeWebPubSub          TargetResourceType = "webpubsub"
	TargetTypeAppInsights        TargetResourceType = "app-insights"
	// compute service as target
	TargetTypeWebApp       TargetResourceType = "webapp"
	TargetTypeFunctionApp  TargetResourceType = "functionapp"
	TargetTypeContainerApp TargetResourceType = "containerapp"
	TargetTypeSpringApp    TargetResourceType = "springapp"
)

func (targetType TargetResourceType) IsValid() bool {
	switch targetType {
	case TargetTypeStorageAccount,
		TargetTypeCosmosDB,
		TargetTypePostgreSqlFlexible,
		TargetTypeMysqlFlexible,
		TargetTypeSql,
		TargetTypeRedis,
		TargetTypeRedisEnterprise,
		TargetTypeKeyVault,
		TargetTypeEventHub,
		TargetTypeAppConfig,
		TargetTypeServiceBus,
		TargetTypeSignalR,
		TargetTypeWebPubSub,
		TargetTypeAppInsights,
		TargetTypeWebApp,
		TargetTypeFunctionApp,
		TargetTypeContainerApp,
		TargetTypeSpringApp:
		return true
	}
	return false
}

func (targetType TargetResourceType) IsComputeService() bool {
	switch targetType {
	case TargetTypeWebApp,
		TargetTypeFunctionApp,
		TargetTypeContainerApp,
		TargetTypeSpringApp:
		return true
	}
	return false
}

// Binding supported store types, used to store binding information
type StoreResourceType string

const (
	StoreTypeAppConfig StoreResourceType = "appconfig"
	// StoreTypeKeyVault  StoreResourceType = "keyvault"
)

func (storeType StoreResourceType) IsValid() bool {
	switch storeType {
	case StoreTypeAppConfig:
		// StoreTypeKeyVault:
		return true
	}
	return false
}

// We will look for the real resource name (Azure resource name) in azd .env file
// according to user provided logic resource name (name in binding config), by
// looking for key `BINDING_RESOURCE_<LogicResourceName>`
const BindingResourceKey = "BINDING_RESOURCE_%s"

// When source resource has sub-resource suffix, we also look for key
// `<BindingResourceKey>_<SubResourceSuffix>`.
// E.g., for SpringApp, we need know not only the service name but also the app name
// to create a service linker resource
var SourceSubResourceSuffix = map[interface{}][]string{
	SourceTypeSpringApp: {"APP"},
}

// When target resource has sub-resource suffix, we also look for key
// `<BindingResourceKey>_<SubResourceSuffix>`.
// E.g., for database service, we need also know the database name beside server name
// to create a service linker resource
var TargetSubResourceSuffix = map[interface{}][]string{
	TargetTypePostgreSqlFlexible: {"DATABASE"},
	TargetTypeMysqlFlexible:      {"DATABASE"},
	TargetTypeSql:                {"DATABASE"},
	TargetTypeRedis:              {"DATABASE"},
	TargetTypeRedisEnterprise:    {"DATABASE"},
	TargetTypeSpringApp:          {"APP"},
}

// When target resource has secret info suffix, we also look for key
// `<BindingResourceKey>_<SecretInfoSuffix>`.
// For some database resources, we need username and secret to create a connection.
// So we expect the secret is stored in a keyvault when running `azd provision`,
// and the user specifies the keyvault secret name when defining the binding.
// This is not a perfect solution, we may improve this in the future if the secret
// can be got from other ways.
var TargetSecretInfoSuffix = map[interface{}][]string{
	TargetTypePostgreSqlFlexible: {"USERNAME", "SECRET_KEYVAULT", "SECRET_NAME"},
	TargetTypeMysqlFlexible:      {"USERNAME", "SECRET_KEYVAULT", "SECRET_NAME"},
	TargetTypeSql:                {"USERNAME", "SECRET_KEYVAULT", "SECRET_NAME"},
}

// Binding store fall back key name.
// We will look for this key name as an app config store name
// if user doesn't provide the binding store resource using `<BindingResourceKey>`
const BindingStoreFallbackKey = "BINDING_STORE_NAME"

// Binding source fall back key name.
// We will look for this key name as the source service name
// if user doesn't provide the binding source resource using `<BindingResourceKey>`
const BindingSourceFallbackKey = "SERVICE_%s_NAME"

// Binding keyvault fall back key name.
// We will look for this key name as the keyvault where we assume the target secret
// is stored, if user doesn't provide the target secret info using `<BindingResourceKey>`
const BindingKeyvaultFallbackKey = "BINDING_SECRET_KEYVAULT"

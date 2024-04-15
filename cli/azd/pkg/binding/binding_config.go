package binding

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicelinker/armservicelinker/v2"
)

// A child property of `services` in azure.yaml, used to define bindings of a service
type BindingConfig struct {
	Name           string             `yaml:"name"`
	TargetType     TargetResourceType `yaml:"targetType"`
	TargetResource string             `yaml:"targetResource"`
	AppConfig      string             `yaml:"appConfig"`
	KeyVault       string             `yaml:"keyVault"`
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
	SourceTypeAppService   SourceResourceType = "appservice"
	SourceTypeContainerApp SourceResourceType = "containerapp"
	SourceTypeFunctionApp  SourceResourceType = "functionapp"
	SourceTypeSpringApp    SourceResourceType = "springapp"
)

func (sourceType SourceResourceType) IsValid() bool {
	switch sourceType {
	case SourceTypeAppService,
		SourceTypeContainerApp,
		SourceTypeFunctionApp,
		SourceTypeSpringApp:
		return true
	}
	return false
}

// Binding supported target resource types
type TargetResourceType string

const (
	TargetTypeAppInsights     TargetResourceType = "appinsights"
	TargetTypeCosmosDB        TargetResourceType = "cosmos"
	TargetTypeEventHub        TargetResourceType = "eventhub"
	TargetTypeMysql           TargetResourceType = "mysql"      // mysql flexible server
	TargetTypePostgreSql      TargetResourceType = "postgresql" // postgresql flexible server
	TargetTypeRedis           TargetResourceType = "redis"
	TargetTypeRedisEnterprise TargetResourceType = "redis-enterprise"
	TargetTypeServiceBus      TargetResourceType = "servicebus"
	TargetTypeSignalR         TargetResourceType = "signalr"
	TargetTypeSql             TargetResourceType = "sql"
	TargetTypeStorageAccount  TargetResourceType = "storage"
	TargetTypeWebPubSub       TargetResourceType = "webpubsub"

	// compute service as target
	TargetTypeContainerApp TargetResourceType = "containerapp"
)

func (targetType TargetResourceType) IsValid() bool {
	switch targetType {
	case TargetTypeAppInsights,
		TargetTypeCosmosDB,
		TargetTypeEventHub,
		TargetTypeMysql,
		TargetTypePostgreSql,
		TargetTypeRedis,
		TargetTypeRedisEnterprise,
		TargetTypeServiceBus,
		TargetTypeSignalR,
		TargetTypeSql,
		TargetTypeStorageAccount,
		TargetTypeWebPubSub,
		TargetTypeContainerApp:
		return true
	}
	return false
}

func (targetType TargetResourceType) IsComputeService() bool {
	switch targetType {
	case TargetTypeContainerApp:
		return true
	}
	return false
}

// Binding supported store types, used to store binding information
type StoreResourceType string

const (
	StoreTypeAppConfig StoreResourceType = "appconfig"
	StoreTypeKeyVault  StoreResourceType = "keyvault"
)

func (storeType StoreResourceType) IsValid() bool {
	switch storeType {
	case StoreTypeAppConfig,
		StoreTypeKeyVault:
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
	TargetTypePostgreSql:      {"DATABASE"},
	TargetTypeMysql:           {"DATABASE"},
	TargetTypeSql:             {"DATABASE"},
	TargetTypeRedis:           {"DATABASE"},
	TargetTypeRedisEnterprise: {"DATABASE"},
}

// When target resource has secret info suffix, we also look for key
// `<BindingResourceKey>_<SecretInfoSuffix>`.
// For some database resources, we need username and secret to create a connection.
// So we expect the secret is stored in a keyvault when running `azd provision`,
// and the user specifies the keyvault secret name when defining the binding.
// This is not a perfect solution, we may improve this in the future if the secret
// can be got from other ways.
var TargetSecretInfoSuffix = map[interface{}][]string{
	TargetTypePostgreSql: {"USERNAME", "KEYVAULT_NAME", "SECRET_NAME"},
	TargetTypeMysql:      {"USERNAME", "KEYVAULT_NAME", "SECRET_NAME"},
	TargetTypeSql:        {"USERNAME", "KEYVAULT_NAME", "SECRET_NAME"},
}

// Binding store fall back key name.
// We will look for this key name as the binding store name
// if user doesn't provide the binding store resource using `<BindingResourceKey>`
var BindingStoreFallbackKey = map[interface{}]string{
	StoreTypeAppConfig: "BINDING_APPCONFIG_NAME",
	StoreTypeKeyVault:  "BINDING_KEYVAULT_NAME",
}

// Binding source fall back key name.
// We will look for this key name as the source service name
// if user doesn't provide the binding source resource using `<BindingResourceKey>`
const BindingSourceFallbackKey = "SERVICE_%s_NAME"

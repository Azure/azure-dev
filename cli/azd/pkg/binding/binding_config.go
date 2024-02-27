package binding

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicelinker/armservicelinker"
)

// A child property of `services` in azure.yaml, used to define bindings of a service
type BindingConfig struct {
	Name           string                      `yaml:"name"`
	TargetType     TargetResourceType          `yaml:"targetType"`
	TargetResource string                      `yaml:"targetResource"`
	StoreType      StoreResourceType           `yaml:"StoreResourceType"`
	StoreResource  string                      `yaml:"storeResource"`
	ClientType     armservicelinker.ClientType `yaml:"clientType"`
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

// Binding supported store types, used to store binding infomations
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
// looking for key `<BindingPrefix>_<LogicResourceName>`
const BindingPrefix = "BINDING_RESOURCE"

// When source resource has sub-resource suffix, we also look for key
// `<BindingPrefix>_<LogicResourceName>_<SubResourceSuffix>`.
// E.g., for SpringApp, we need know not only the service name but also the app name
var SourceSubResourceSuffix = map[interface{}][]string{
	SourceTypeSpringApp: {"APP"},
}

// When source resource has scope suffix, we also look for key
// `<BindingPrefix>_<LogicResourceName>_<ScopeSuffix>`.
// E.g., for ContainerApp, we need also know the container name
var SourceScopeSuffix = map[interface{}][]string{
	SourceTypeContainerApp: {"CONTAINER"},
}

// When target resource has sub-resource suffix, we also look for key
// `<BindingPrefix>_<LogicResourceName>_<SubResourceSuffix>`.
// E.g., for database service, we need also know the database name (besides server name)
var TargetSubResourceSuffix = map[interface{}][]string{
	TargetTypePostgreSqlFlexible: {"DATABASE"},
	TargetTypeMysqlFlexible:      {"DATABASE"},
	TargetTypeSql:                {"DATABASE"},
	TargetTypeRedis:              {"DATABASE"},
	TargetTypeRedisEnterprise:    {"DATABASE"},
}

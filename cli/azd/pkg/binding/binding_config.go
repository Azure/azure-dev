package binding

import "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicelinker/armservicelinker"

type BindingConfig struct {
	Name           string                      `yaml:"name"`
	SourceType     SourceResourceType          `yaml:"sourceType"`
	SourceResource string                      `yaml:"sourceResource"`
	TargetType     TargetResourceType          `yaml:"targetType"`
	TargetResource string                      `yaml:"targetResource"`
	StoreType      StoreType                   `yaml:"storeType"`
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

// Binding supported store types, which used to store the binding infomations
type StoreType string

const (
	StoreTypeAppConfig StoreType = "appconfig"
)

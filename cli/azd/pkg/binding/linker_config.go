package binding

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicelinker/armservicelinker"
)

// Service linker facing (each binding corresponds to a linker resource)
// LinkerConfig includes all required infomation used to create a service linker resource
type LinkerConfig struct {
	Name             string
	SourceResourceId string
	TargetResourceId string
	StoreResourceId  string
	DBUserName       string
	DBSecret         string
	ClientType       armservicelinker.ClientType
}

// Resource id formats supported by service linker as source resource
var SourceResourceIdFormats = map[interface{}]string{
	SourceTypeWebApp:       "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Web/sites/%s",
	SourceTypeFunctionApp:  "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Web/sites/%s",
	SourceTypeContainerApp: "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s",
	SourceTypeSpringApp:    "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.AppPlatform/Spring/%s/apps/%s",
}

// Resource id formats supported by service linker as target resource
var TargetResourceIdFormats = map[interface{}]string{
	TargetTypeStorageAccount:     "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Storage/storageAccounts/%s",
	TargetTypeCosmosDB:           "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DocumentDB/databaseAccounts/%s",
	TargetTypePostgreSqlFlexible: "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DBforPostgreSQL/flexibleServers/%s/databases/%s",
	TargetTypeMysqlFlexible:      "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DBforMySQL/flexibleServers/%s/databases/%s",
	TargetTypeSql:                "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Sql/servers/%s/databases/%s",
	TargetTypeRedis:              "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Cache/redis/%s/databases/%s",
	TargetTypeRedisEnterprise:    "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Cache/redisEnterprise/%s/databases/%s",
	TargetTypeKeyVault:           "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.KeyVault/vaults/%s",
	TargetTypeEventHub:           "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.EventHub/namespaces/%s",
	TargetTypeAppConfig:          "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.AppConfiguration/configurationStores/%s",
	TargetTypeServiceBus:         "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ServiceBus/namespaces/%s",
	TargetTypeSignalR:            "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.SignalRService/SignalR/%s",
	TargetTypeWebPubSub:          "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.SignalRService/WebPubSub/%s",
	TargetTypeAppInsights:        "/subscriptions/%s/resourceGroups/%s/providers/microsoft.insights/components/%s",
	// compute service as target, the format may be different from source resource
	TargetTypeWebApp:       "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Web/sites/%s",
	TargetTypeFunctionApp:  "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Web/sites/%s",
	TargetTypeContainerApp: "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s",
	TargetTypeSpringApp:    "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.AppPlatform/Spring/%s",
}

// Resource id formats supported by service linker as binding info store
var StoreResourceIdFormats = map[interface{}]string{
	StoreTypeAppConfig: "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.AppConfiguration/configurationStores/%s",
	// StoreTypeKeyVault:  "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.KeyVault/vaults/%s",
}

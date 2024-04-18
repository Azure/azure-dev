package binding

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicelinker/armservicelinker/v2"
)

// Service linker facing (each binding corresponds to a linker resource)
// LinkerConfig includes all required information used to create a service linker resource
type LinkerConfig struct {
	Name        string
	SourceType  SourceResourceType
	SourceId    string
	TargetType  TargetResourceType
	TargetId    string
	AppConfigId string
	KeyVaultId  string
	DBUserName  string
	DBSecret    string
	ClientType  armservicelinker.ClientType
	CustomKeys  map[string]string
}

// Resource id formats supported by service linker as source resource
var SourceResourceIdFormats = map[interface{}]string{
	SourceTypeAppService:   "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Web/sites/%s",
	SourceTypeContainerApp: "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s",
	SourceTypeFunctionApp:  "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Web/sites/%s",
	SourceTypeSpringApp:    "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.AppPlatform/Spring/%s/apps/%s",
}

// Resource id formats supported by service linker as target resource
var TargetResourceIdFormats = map[interface{}]string{
	TargetTypeAppInsights: "/subscriptions/%s/resourceGroups/%s/providers/microsoft.insights/components/%s",
	TargetTypeCosmosDB:    "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DocumentDB/databaseAccounts/%s",
	TargetTypeEventHub:    "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.EventHub/namespaces/%s",
	TargetTypeMysql: "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DBforMySQL/" +
		"flexibleServers/%s/databases/%s",
	TargetTypePostgreSql: "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DBforPostgreSQL/" +
		"flexibleServers/%s/databases/%s",
	TargetTypeRedis: "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Cache/redis/%s/databases/%s",
	TargetTypeRedisEnterprise: "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Cache/" +
		"redisEnterprise/%s/databases/%s",
	TargetTypeServiceBus:     "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ServiceBus/namespaces/%s",
	TargetTypeSignalR:        "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.SignalRService/SignalR/%s",
	TargetTypeSql:            "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Sql/servers/%s/databases/%s",
	TargetTypeStorageAccount: "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Storage/storageAccounts/%s",
	TargetTypeWebPubSub:      "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.SignalRService/WebPubSub/%s",

	// compute service as target, the format may be different from source resource
	TargetTypeContainerApp: "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s",
}

// Resource id formats supported by service linker as binding info store
var StoreResourceIdFormats = map[interface{}]string{
	StoreTypeAppConfig: "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.AppConfiguration/configurationStores/%s",
	StoreTypeKeyVault:  "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.KeyVault/vaults/%s",
}

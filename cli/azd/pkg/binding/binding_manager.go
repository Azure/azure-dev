package binding

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicelinker/armservicelinker"
)

// BindingManager exposes operations for managing bindings in azure.yaml. Each binding item correspond to a
// service linker resource
type BindingManager interface {
	CreateBinding(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		binding *BindingConfig,
	) error
	CreateBindings(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		bindings []*BindingConfig,
	) error
	DeleteBinding(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		binding *BindingConfig,
	) error
	DeleteBindings(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		bindings []*BindingConfig,
	) error
}

type bindingManager struct {
	linkerService ServiceLinkerService
}

func NewBindingManager(
	linkerService ServiceLinkerService,
) BindingManager {
	return &bindingManager{
		linkerService: linkerService,
	}
}

// Create bindings
func (bm *bindingManager) CreateBindings(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	bindings []*BindingConfig,
) error {
	err := validateBindingConfigs(bindings)
	if err != nil {
		return err
	}

	for _, binding := range bindings {
		err := bm.CreateBinding(ctx, subscriptionId, resourceGroupName, binding)
		if err != nil {
			return err
		}
	}
	return nil
}

// Create a binding
func (bm *bindingManager) CreateBinding(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	binding *BindingConfig,
) error {
	sourceResourceId, _ := formatResourceId(subscriptionId, resourceGroupName, binding.SourceResource, SourceResourceIdFormats[binding.SourceType])
	targetResourceId, _ := formatResourceId(subscriptionId, resourceGroupName, binding.TargetResource, TargetResourceIdFormats[binding.TargetType])

	_, err := bm.linkerService.Create(ctx, subscriptionId, sourceResourceId, binding.Name, targetResourceId, binding.ClientType)
	return err
}

// Delete bindings
func (bm *bindingManager) DeleteBindings(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	bindings []*BindingConfig,
) error {
	for _, binding := range bindings {
		err := bm.DeleteBinding(ctx, subscriptionId, resourceGroupName, binding)
		if err != nil {
			return err
		}
	}
	return nil
}

// Delete a binding
func (bm *bindingManager) DeleteBinding(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	binding *BindingConfig,
) error {
	sourceResourceId, _ := formatResourceId(subscriptionId, resourceGroupName, binding.SourceResource, SourceResourceIdFormats[binding.SourceType])

	err := bm.linkerService.Delete(ctx, subscriptionId, sourceResourceId, binding.Name)
	return err
}

// Check if user provided binding configs are valid
func validateBindingConfigs(
	bindings []*BindingConfig,
) error {
	for _, binding := range bindings {
		err := validateBindingConfig(binding)
		if err != nil {
			return err
		}
	}

	return nil
}

// Check if a single binding config is valid
func validateBindingConfig(
	binding *BindingConfig,
) error {
	// validate binding name
	err := validateBindingName(binding.Name)
	if err != nil {
		return err
	}

	// validate source resource type
	err = validateSourceType(binding.SourceType)
	if err != nil {
		return err
	}

	// validate target resource type
	err = validateTargetType(binding.TargetType)
	if err != nil {
		return err
	}

	// validate client type
	err = validateClientType(binding.ClientType)
	if err != nil {
		return err
	}

	// validate store type
	err = validateStoreType(binding.StoreType)
	if err != nil {
		return err
	}

	// validate source resource in the binding
	err = validateResourceId(SourceResourceIdFormats[binding.SourceType], binding.SourceResource)
	if err != nil {
		return err
	}

	// validate target resource in the binding
	err = validateResourceId(TargetResourceIdFormats[binding.TargetType], binding.TargetResource)
	if err != nil {
		return err
	}

	// validate store resource in the binding
	err = validateResourceId(StoreResourceIdFormats[binding.StoreType], binding.StoreResource)
	if err != nil {
		return err
	}

	return nil
}

// Check if the provided binding name is valid
func validateBindingName(
	bindingName string,
) error {
	// Binding name should align with service linker naming rule
	pattern := "^[A-Za-z0-9\\._]{1,60}$"
	regex := regexp.MustCompile(pattern)
	match := regex.MatchString(bindingName)

	if !match {
		return fmt.Errorf("the provided binding name: '%s' is invalid. "+
			"Binding name can only contain letters, numbers (0-9), periods ('.'), and underscores ('_'). "+
			"The length must not be more than 60 characters", bindingName)
	}

	return nil
}

// Check if the provided resource name could match the resource id format
func validateResourceId(
	resourceIdFormat string,
	resourceName string,
) error {
	// Split resource name as there may be sub-resources
	components := strings.Split(resourceName, "/")

	// Number of placeholders in resource id format should match components in resource name
	if (len(components) + 2) != strings.Count(resourceIdFormat, "%s") {
		return fmt.Errorf("the provided resource name: '%s' does not match the resource id format: '%s'",
			resourceName, resourceIdFormat)
	}

	return nil
}

// Check if the source resource type is supported
func validateSourceType(
	sourceType SourceResourceType,
) error {
	switch sourceType {
	case SourceTypeWebApp, SourceTypeFunctionApp, SourceTypeContainerApp, SourceTypeSpringApp:
		return nil
	}

	return fmt.Errorf("source resource type: '%s' is not supported", sourceType)
}

// Check if the target resource type is supported
func validateTargetType(
	targetType TargetResourceType,
) error {
	switch targetType {
	case TargetTypeStorageAccount, TargetTypeCosmosDB, TargetTypePostgreSqlFlexible, TargetTypeMysqlFlexible,
		TargetTypeSql, TargetTypeRedis, TargetTypeRedisEnterprise, TargetTypeKeyVault, TargetTypeEventHub,
		TargetTypeAppConfig, TargetTypeServiceBus, TargetTypeSignalR, TargetTypeWebPubSub, TargetTypeAppInsights,
		TargetTypeWebApp, TargetTypeFunctionApp, TargetTypeContainerApp, TargetTypeSpringApp:
		return nil
	}

	return fmt.Errorf("target resource type: '%s' is not supported", targetType)
}

// Check if the source resource type is supported
func validateClientType(
	clientType armservicelinker.ClientType,
) error {
	for _, possibleValue := range armservicelinker.PossibleClientTypeValues() {
		if clientType == possibleValue {
			return nil
		}
	}

	return fmt.Errorf("client type: '%s' is not supported", clientType)
}

// Check if the source resource type is supported
func validateStoreType(
	storeType StoreType,
) error {
	switch storeType {
	case StoreTypeAppConfig:
		return nil
	}

	return fmt.Errorf("store type: '%s' is not supported", storeType)
}

// Resource id formats supported by service linker as source resource
var SourceResourceIdFormats = map[SourceResourceType]string{
	SourceTypeWebApp:       "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Web/sites/%s",
	SourceTypeFunctionApp:  "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Web/sites/%s",
	SourceTypeContainerApp: "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s",
	SourceTypeSpringApp:    "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.AppPlatform/Spring/%s/apps/%s",
}

// Resource id formats supported by service linker as target resource
var TargetResourceIdFormats = map[TargetResourceType]string{
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
	TargetTypeWebApp:             "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Web/sites/%s",
	TargetTypeFunctionApp:        "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Web/sites/%s",
	TargetTypeContainerApp:       "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s",
	TargetTypeSpringApp:          "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.AppPlatform/Spring/%s/apps/%s",
}

// Resource id formats supported by service linker as binding info store
var StoreResourceIdFormats = map[StoreType]string{
	StoreTypeAppConfig: "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.AppConfiguration/configurationStores/%s",
}

// Format source and target resource id according to resource id format
func formatResourceId(
	subscriptionId string,
	resourceGroupName string,
	resourceName string,
	resourceIdForamt string,
) (string, error) {
	validateResourceId(resourceIdForamt, resourceName)

	// Replace subscription, resource group and resource name placeholders in resource id
	components := append([]string{subscriptionId, resourceGroupName}, strings.Split(resourceName, "/")...)
	interfaceComponents := make([]interface{}, len(components))
	for i, v := range components {
		interfaceComponents[i] = v
	}

	return fmt.Sprintf(resourceIdForamt, interfaceComponents...), nil
}

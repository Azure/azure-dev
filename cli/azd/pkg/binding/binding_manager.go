package binding

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicelinker/armservicelinker"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
)

// BindingManager exposes operations for managing service bindings in `azure.yaml` file
// A binding corresponds to a service linker resource.
type BindingManager interface {
	ValidateBindingConfigs(
		sourceType SourceResourceType,
		sourceResource string,
		bindingConfigs []*BindingConfig,
	) error
	CreateBindings(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		sourceType SourceResourceType,
		sourceResource string,
		bindingConfigs []*BindingConfig,
	) error
	DeleteBindings(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		sourceType SourceResourceType,
		sourceResource string,
		bindingConfigs []*BindingConfig,
	) error
}

type bindingManager struct {
	linkerManager LinkerManager
	env           *environment.Environment
}

func NewBindingManager(
	linkerManager LinkerManager,
	env *environment.Environment,
) BindingManager {
	return &bindingManager{
		linkerManager: linkerManager,
		env:           env,
	}
}

// Validate binding source service and binding configs before creating bindings,
// to fail earlier in case there are any user errors
func (bm *bindingManager) ValidateBindingConfigs(
	sourceType SourceResourceType,
	sourceResource string,
	bindingConfigs []*BindingConfig,
) error {
	// validate binding source type and source resource is specified in .env file
	err := validateBindingSource(sourceType, sourceResource, bm.env.Dotenv())
	if err != nil {
		return err
	}

	// validate other binding info: tagret resource, store resource, binding name ...
	for _, bindingConfig := range bindingConfigs {
		err := validateBindingConfig(bindingConfig, bm.env.Dotenv())
		if err != nil {
			return err
		}
	}

	return nil
}

// Create all bindings of a service (each binding corresponds to a service linker resource)
func (bm *bindingManager) CreateBindings(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	sourceType SourceResourceType,
	sourceResource string,
	bindingConfigs []*BindingConfig,
) error {
	// collect Azure resource names from .env file, converting binding configs to linker configs
	linkerConfigs, err := convertBindingsToLinkers(
		subscriptionId, resourceGroupName, sourceType, sourceResource, bindingConfigs, bm.env.Dotenv())
	if err != nil {
		return err
	}

	// create service linker resource for each binding
	for _, linkerConfig := range linkerConfigs {
		_, err := bm.linkerManager.Create(ctx, subscriptionId, linkerConfig)
		if err != nil {
			return err
		}
	}
	return nil
}

// Delete all bindings of a service (each binding corresponds to a service linker resource)
func (bm *bindingManager) DeleteBindings(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	sourceType SourceResourceType,
	sourceResource string,
	bindingConfigs []*BindingConfig,
) error {
	// collect Azure resource names from .env file, converting binding configs to linker configs
	linkerConfigs, err := convertBindingsToLinkers(
		subscriptionId, resourceGroupName, sourceType, sourceResource, bindingConfigs, bm.env.Dotenv())
	if err != nil {
		return err
	}

	// delete service linker resource for each binding
	for _, linkerConfig := range linkerConfigs {
		err := bm.linkerManager.Delete(ctx, subscriptionId, linkerConfig)
		if err != nil {
			return err
		}
	}
	return nil
}

// Convert binding configs to linker configs. Linker configs collects all info required to create
// service linker resources
func convertBindingsToLinkers(
	subscriptionId string,
	resourceGroupName string,
	sourceType SourceResourceType,
	sourceResource string,
	bindingConfigs []*BindingConfig,
	env map[string]string,
) ([]*LinkerConfig, error) {
	linkerConfigs := []*LinkerConfig{}

	sourceId, err := getResourceId(subscriptionId, resourceGroupName,
		sourceType, sourceResource, env)
	if err != nil {
		return linkerConfigs, err
	}

	for _, bindingConfig := range bindingConfigs {
		targetId, err := getResourceId(subscriptionId, resourceGroupName,
			bindingConfig.TargetType, bindingConfig.TargetResource, env)
		if err != nil {
			return linkerConfigs, err
		}

		scope, err := getScope(sourceType, sourceResource, env)
		if err != nil {
			return linkerConfigs, err
		}

		storeId, err := getResourceId(subscriptionId, resourceGroupName,
			bindingConfig.StoreType, bindingConfig.StoreResource, env)
		if err != nil {
			return linkerConfigs, err
		}

		linkerConfigs = append(linkerConfigs, &LinkerConfig{
			Name:             getLinkerName(bindingConfig),
			SourceResourceId: sourceId,
			Scope:            scope,
			TargetResourceId: targetId,
			StoreResourceId:  storeId,
			ClientType:       bindingConfig.ClientType,
		})
	}

	return linkerConfigs, nil
}

// Get linker name from binding config: use user provided name if specified, or else, generate
// a linker name according to the binding config.
func getLinkerName(
	bindingConfig *BindingConfig,
) string {
	// use user provided name if specified
	if len(bindingConfig.Name) > 0 {
		return bindingConfig.Name
	}

	// or else, use `<targetType>_<targetResource>` (also removing all invalid characters)
	linkerName := string(bindingConfig.TargetType) + "_" + string(bindingConfig.TargetResource)
	regex := regexp.MustCompile(`^[^A-Za-z0-9\\._]$`)
	linkerName = regex.ReplaceAllString(linkerName, "")

	// service linker resource name length limit
	return linkerName[:50]
}

// Collect resource names in .env file and format resource id according to resource id format
// Can be used for any of a SourceResourceType, TargetResourceType or a StoreResourceType
func getResourceId(
	subscriptionId string,
	resourceGroupName string,
	resourceType interface{},
	resourceName string,
	env map[string]string,
) (string, error) {
	resourceIdFormat, resourceKeys := "", []string{}

	// get resource id format and expected keys in .env file according to resource type
	switch resourceType.(type) {
	case SourceResourceType:
		resourceIdFormat = SourceResourceIdFormats[resourceType]
		resourceKeys = getExpectedKeysInEnv(resourceType, resourceName, SourceSubResourceSuffix[resourceType])
	case TargetResourceType:
		resourceIdFormat = TargetResourceIdFormats[resourceType]
		resourceKeys = getExpectedKeysInEnv(resourceType, resourceName, TargetSubResourceSuffix[resourceType])
	case StoreResourceType:
		resourceIdFormat = StoreResourceIdFormats[resourceType]
		resourceKeys = getExpectedKeysInEnv(resourceType, resourceName, nil)
	}

	resourceValues, err := getExpectedValuesInEnv(resourceKeys, env)
	if err != nil {
		return "", err
	}

	return formatResourceId(subscriptionId, resourceGroupName, resourceValues, resourceIdFormat)
}

// Get scope value from .env file according to source resource type and source resource name
func getScope(
	sourceType SourceResourceType,
	sourceResource string,
	env map[string]string,
) (string, error) {
	scopeKeys := getExpectedKeysInEnv(sourceType, sourceResource, SourceScopeSuffix[sourceType])

	scopeValues, err := getExpectedValuesInEnv(scopeKeys, env)
	if err != nil {
		return "", err
	}

	if len(scopeValues) != 1 {
		return "", fmt.Errorf("multiple scope values found for source resource: '%s'", sourceResource)
	}

	return scopeValues[0], nil
}

// Check if the binding source service is valid, including the hosting service type and expected
// source service related environment variables in .env file
func validateBindingSource(
	sourceType SourceResourceType,
	sourceResource string,
	env map[string]string,
) error {
	if !sourceType.IsValid() {
		return fmt.Errorf("source resource type: '%s' is not supported", sourceType)
	}

	sourceKeys := getExpectedKeysInEnv(sourceType, sourceResource, SourceSubResourceSuffix[sourceType])
	_, err := getExpectedValuesInEnv(sourceKeys, env)
	if err != nil {
		return err
	}

	return nil
}

// Check if a binding config is valid, including the tagret resource, store resource, client type
// and their expected environment variables in .env file
func validateBindingConfig(
	binding *BindingConfig,
	env map[string]string,
) error {
	// validate binding name
	if len(binding.Name) > 0 {
		err := validateBindingName(binding.Name)
		if err != nil {
			return err
		}
	}

	// validate target resource type
	if !binding.TargetType.IsValid() {
		return fmt.Errorf("target resource type: '%s' is not supported", binding.TargetType)
	}

	// validate store resource type
	if !binding.StoreType.IsValid() {
		return fmt.Errorf("store resource type: '%s' is not supported", binding.StoreType)
	}

	// validate client type
	if !isValidClientType(binding.ClientType) {
		return fmt.Errorf("client type: '%s' is not supported", binding.ClientType)
	}

	// validate target resource in the binding
	targetKeys := getExpectedKeysInEnv(binding.TargetType, binding.TargetResource,
		TargetSubResourceSuffix[binding.TargetType])
	_, err := getExpectedValuesInEnv(targetKeys, env)
	if err != nil {
		return err
	}

	// validate store resource in the binding
	storeKeys := getExpectedKeysInEnv(binding.StoreType, binding.StoreResource, nil)
	_, err = getExpectedValuesInEnv(storeKeys, env)
	if err != nil {
		return err
	}

	return nil
}

// Check if the provided binding name is valid as service linker resource name
func validateBindingName(
	bindingName string,
) error {
	// service linker resource naming rule
	regex := regexp.MustCompile(`^[A-Za-z0-9\\._]{1,60}$`)
	match := regex.MatchString(bindingName)

	if !match {
		return fmt.Errorf("the provided binding name: '%s' is invalid. Binding name can "+
			"only contain letters, numbers (0-9), periods ('.'), and underscores ('_'). "+
			"The length must not be more than 60 characters", bindingName)
	}

	return nil
}

// Get expected key names in .env file for a resource type and resource name
func getExpectedKeysInEnv(
	resourceType interface{},
	resourceName string,
	keySuffixes []string,
) []string {
	resourceKey := BindingPrefix + "_" + resourceName
	expectedKeys := []string{resourceKey}

	if len(keySuffixes) > 0 {
		for _, suffix := range keySuffixes {
			expectedKeys = append(expectedKeys, resourceKey+"_"+suffix)
		}
	}

	return expectedKeys
}

// Get values from .env file according to expected key names
func getExpectedValuesInEnv(
	expectedKeys []string,
	env map[string]string,
) ([]string, error) {
	expectedValues := []string{}

	for _, key := range expectedKeys {
		value, exists := env[key]
		if exists {
			expectedValues = append(expectedValues, value)
		} else {
			return nil, fmt.Errorf("expected key: '%s' is not found in .env file", key)
		}
	}

	return expectedValues, nil
}

// Check if the client type is valid for service linker resource
func isValidClientType(
	clientType armservicelinker.ClientType,
) bool {
	for _, value := range armservicelinker.PossibleClientTypeValues() {
		if clientType == value {
			return true
		}
	}
	return false
}

// Replace subscription, resource group and resource name placeholders in resource id format, returing
// the formatted resource id
func formatResourceId(
	subscriptionId string,
	resourceGroupName string,
	resourceNames []string,
	resourceIdFormat string,
) (string, error) {
	components := append([]string{subscriptionId, resourceGroupName}, resourceNames...)
	if (len(components)) != strings.Count(resourceIdFormat, "%s") {
		return "", fmt.Errorf("resource id format is not matched: '%s'", resourceIdFormat)
	}

	interfaceComponents := make([]interface{}, len(components))
	for i, v := range components {
		interfaceComponents[i] = v
	}

	return fmt.Sprintf(resourceIdFormat, interfaceComponents...), nil
}

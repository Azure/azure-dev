package binding

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
)

// BindingManager exposes operations for managing service bindings in `azure.yaml` file
// A binding corresponds to a service linker resource.
type BindingManager interface {
	ValidateBindingConfigs(
		bindingSource *BindingSource,
		bindingConfigs []*BindingConfig,
	) error
	CreateBindings(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		bindingSource *BindingSource,
		bindingConfigs []*BindingConfig,
	) error
}

type bindingManager struct {
	linkerManager LinkerManager
	kvs           keyvault.KeyVaultService
	env           *environment.Environment
	console       input.Console
}

func NewBindingManager(
	linkerManager LinkerManager,
	kvs keyvault.KeyVaultService,
	env *environment.Environment,
	console input.Console,
) BindingManager {
	return &bindingManager{
		linkerManager: linkerManager,
		kvs:           kvs,
		env:           env,
		console:       console,
	}
}

// Validate binding source service and binding configs before creating bindings,
// to fail earlier in case there are any user errors
func (bm *bindingManager) ValidateBindingConfigs(
	bindingSource *BindingSource,
	bindingConfigs []*BindingConfig,
) error {
	// validate binding source type and source resource info in .env file
	err := validateBindingSource(bindingSource, bm.env.Dotenv())
	if err != nil {
		return err
	}

	// validate other binding info: target resource, store resource, binding name ...
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
	bindingSource *BindingSource,
	bindingConfigs []*BindingConfig,
) error {
	// get binding resource info from .env file, converting binding configs into linker configs
	linkerConfigs, err := convertBindingsToLinkers(
		ctx, bm.kvs, subscriptionId, resourceGroupName, bindingSource, bindingConfigs, bm.env.Dotenv())
	if err != nil {
		return err
	}

	// create service linker resource for each binding
	for index, linkerConfig := range linkerConfigs {
		stepMessage := fmt.Sprintf("Create bindings for service %s (%s: %s)",
			bindingSource.SourceResource, bindingConfigs[index].TargetType, bindingConfigs[index].TargetResource)
		bm.console.ShowSpinner(ctx, stepMessage, input.Step)

		_, err := bm.linkerManager.Create(ctx, subscriptionId, linkerConfig)
		if err != nil {
			bm.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return err
		}
	}
	return nil
}

// Check if the binding source service is valid, including the hosting service type and expected
// source service related environment variables in .env file
func validateBindingSource(
	bindingSource *BindingSource,
	env map[string]string,
) error {
	if !bindingSource.SourceType.IsValid() {
		return fmt.Errorf("source resource type: '%s' is not supported", bindingSource.SourceType)
	}

	// validate binding source resource info in the .env file
	_, err := getSourceResourceInfo(bindingSource.SourceType, bindingSource.SourceResource, env)
	return err
}

// Check if a binding config is valid, including the target resource, store resource
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

	// validate target resource info in the binding
	_, err := getTargetResourceInfo(binding.TargetType, binding.TargetResource, env)
	if err != nil {
		return err
	}

	// validate store resource info in the binding
	_, err = getStoreResourceInfo(StoreTypeAppConfig, binding.AppConfig, env)
	if err != nil {
		return err
	}

	// validate target secret info in the binding
	if TargetSecretInfoSuffix[binding.TargetType] != nil {
		_, err = getTargetSecretInfo(binding.TargetType, binding.TargetResource, env)
		if err != nil {
			return err
		}
	}

	return err
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

// Convert binding configs to linker configs. Linker configs include all info required to create
// service linker resources
func convertBindingsToLinkers(
	ctx context.Context,
	kvs keyvault.KeyVaultService,
	subscriptionId string,
	resourceGroupName string,
	bindingSource *BindingSource,
	bindingConfigs []*BindingConfig,
	env map[string]string,
) ([]*LinkerConfig, error) {
	linkerConfigs := []*LinkerConfig{}

	sourceId, err := getResourceId(subscriptionId, resourceGroupName, bindingSource.SourceType,
		bindingSource.SourceResource, env)
	if err != nil {
		return linkerConfigs, err
	}

	for _, bindingConfig := range bindingConfigs {
		targetId, err := getResourceId(subscriptionId, resourceGroupName, bindingConfig.TargetType,
			bindingConfig.TargetResource, env)
		if err != nil {
			return linkerConfigs, err
		}

		appconfigId, err := getResourceId(subscriptionId, resourceGroupName, StoreTypeAppConfig,
			bindingConfig.AppConfig, env)
		if err != nil {
			return linkerConfigs, err
		}

		// For some database target resources, we need the secret to create a connection.
		// So we expect the secret is stored in a keyvault when running `azd provision`,
		// and the user specifies the keyvault secret name when defining the binding.
		userName, secret := "", ""
		if TargetSecretInfoSuffix[bindingConfig.TargetType] != nil {
			userName, secret, err = getTargetSecret(ctx, kvs, subscriptionId, bindingConfig.TargetType,
				bindingConfig.TargetResource, env)
			if err != nil {
				return linkerConfigs, err
			}
		}

		// When binding has sensitive configs (for example, the storage account key),
		// use could specify a keyvault to save the secret, and the appconfig will reference
		// the keyvault secret.
		keyvaultId := ""
		if bindingConfig.KeyVault != "" {
			keyvaultId, err = getResourceId(subscriptionId, resourceGroupName, StoreTypeKeyVault,
				bindingConfig.KeyVault, env)
			if err != nil {
				return linkerConfigs, err
			}
		}

		linkerConfigs = append(linkerConfigs, &LinkerConfig{
			Name:        getLinkerName(bindingConfig),
			SourceType:  bindingSource.SourceType,
			SourceId:    sourceId,
			TargetType:  bindingConfig.TargetType,
			TargetId:    targetId,
			AppConfigId: appconfigId,
			KeyVaultId:  keyvaultId,
			DBUserName:  userName,
			DBSecret:    secret,
			ClientType:  bindingSource.ClientType,
		})
	}

	return linkerConfigs, nil
}

// Get linker name from binding config: use user provided name first. If not specified, generate
// a linker name according to the binding config
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
	return linkerName[:min(len(linkerName), 50)]
}

// Get target resource's username and secret from the .env file and the keyvault
// secret specified in the .env file
func getTargetSecret(
	ctx context.Context,
	kvs keyvault.KeyVaultService,
	subscriptionId string,
	resourceType TargetResourceType,
	resourceName string,
	env map[string]string,
) (string, string, error) {
	secretInfo, err := getTargetSecretInfo(resourceType, resourceName, env)
	if err != nil {
		return "", "", err
	}

	keyvaultName, secretName := secretInfo[1], secretInfo[2]
	cliSecret, err := kvs.GetKeyVaultSecret(ctx, subscriptionId, keyvaultName, secretName)
	if err != nil {
		return "", "", fmt.Errorf("failed to get secret `%s` from keyvault `%s`, please"+
			" check whether the secret exists or not", keyvaultName, secretName)
	}

	// we assume the secret is whether a connection string or a raw password
	// that was stored in the keyvault when running `azd provision`
	return secretInfo[0], retrievePassword(cliSecret.Value), nil
}

// Get real resource names in .env file and format resource id according to resource id format
// Can be used for any of a SourceResourceType, TargetResourceType or a StoreResourceType
func getResourceId(
	subscriptionId string,
	resourceGroupName string,
	resourceType interface{},
	resourceName string,
	env map[string]string,
) (string, error) {
	resourceIdFormat, resourceInfo := "", []string{}
	var err error = nil

	switch t := resourceType.(type) {
	case SourceResourceType:
		resourceIdFormat = SourceResourceIdFormats[t]
		resourceInfo, err = getSourceResourceInfo(t, resourceName, env)
	case TargetResourceType:
		resourceIdFormat = TargetResourceIdFormats[t]
		resourceInfo, err = getTargetResourceInfo(t, resourceName, env)
	case StoreResourceType:
		resourceIdFormat = StoreResourceIdFormats[t]
		resourceInfo, err = getStoreResourceInfo(t, resourceName, env)
	}

	if err != nil {
		return "", err
	}

	return formatResourceId(subscriptionId, resourceGroupName, resourceInfo, resourceIdFormat)
}

// Get real resource names in .env file according to the source resource type and resource name
func getSourceResourceInfo(
	resourceType SourceResourceType,
	resourceName string,
	env map[string]string,
) ([]string, error) {
	resourceKey := fmt.Sprintf(BindingResourceKey, strings.ToUpper(resourceName))
	expectedKeys := []string{resourceKey}

	// if source resource has sub-resource, we also look for the sub-resource keys
	if keySuffixes, ok := SourceSubResourceSuffix[resourceType]; ok {
		for _, suffix := range keySuffixes {
			expectedKeys = append(expectedKeys, resourceKey+"_"+suffix)
		}
	}

	// if expected keys for source does not exist, we look for the fallback key
	resourceInfo, err := getExpectedValuesInEnv(expectedKeys, env)
	if err != nil && SourceSubResourceSuffix[resourceType] == nil {
		expectedKeys = []string{fmt.Sprintf(BindingSourceFallbackKey, strings.ToUpper(resourceName))}
		resourceInfo, err = getExpectedValuesInEnv(expectedKeys, env)
	}

	return resourceInfo, err
}

// Get real resource names in .env file according to the target resource type and resource name
func getTargetResourceInfo(
	resourceType TargetResourceType,
	resourceName string,
	env map[string]string,
) ([]string, error) {
	resourceKey := fmt.Sprintf(BindingResourceKey, strings.ToUpper(resourceName))
	expectedKeys := []string{resourceKey}

	// if target resource has sub-resource, we also look for the sub-resource keys
	if keySuffixes, ok := TargetSubResourceSuffix[resourceType]; ok {
		for _, suffix := range keySuffixes {
			expectedKeys = append(expectedKeys, resourceKey+"_"+suffix)
		}
	}

	// if target is compute resource and expected keys not exist, we look for compute fallback key
	resourceInfo, err := getExpectedValuesInEnv(expectedKeys, env)
	if err != nil && resourceType.IsComputeService() && TargetSubResourceSuffix[resourceType] == nil {
		expectedKeys = []string{fmt.Sprintf(BindingSourceFallbackKey, strings.ToUpper(resourceName))}
		resourceInfo, err = getExpectedValuesInEnv(expectedKeys, env)
	}

	return resourceInfo, err
}

// Get real resource names in .env file according to the store resource type and resource name
func getStoreResourceInfo(
	storeType StoreResourceType,
	resourceName string,
	env map[string]string,
) ([]string, error) {
	resourceKey := fmt.Sprintf(BindingResourceKey, strings.ToUpper(resourceName))
	expectedKeys := []string{resourceKey}

	// if expected keys for store does not exist, we look for the fallback key
	resourceInfo, err := getExpectedValuesInEnv(expectedKeys, env)
	if err != nil {
		expectedKeys = []string{BindingStoreFallbackKey[storeType]}
		resourceInfo, err = getExpectedValuesInEnv(expectedKeys, env)
	}

	return resourceInfo, err
}

// Get real secret info in .env file according to the target resource type and resource name
func getTargetSecretInfo(
	resourceType TargetResourceType,
	resourceName string,
	env map[string]string,
) ([]string, error) {
	resourceKey := fmt.Sprintf(BindingResourceKey, strings.ToUpper(resourceName))
	expectedKeys := []string{}

	if keySuffixes, ok := TargetSecretInfoSuffix[resourceType]; ok {
		for _, suffix := range keySuffixes {
			expectedKeys = append(expectedKeys, resourceKey+"_"+suffix)
		}

		// if expected keys for secret does not exist, we look for the fallback key
		resourceInfo, err := getExpectedValuesInEnv(expectedKeys, env)
		if err != nil {
			expectedKeys[1] = BindingStoreFallbackKey[StoreTypeKeyVault]
			resourceInfo, err = getExpectedValuesInEnv(expectedKeys, env)
		}

		return resourceInfo, err
	}

	return []string{}, fmt.Errorf("target resource type: '%s' doesn't have secret info", resourceType)
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
			return nil, fmt.Errorf("expected key '%s' is not found in .env file", key)
		}
	}

	return expectedValues, nil
}

// Replace subscription, resource group and resource name placeholders in resource id format, returning
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

// Retrieve password from a connection string of SQL, MySQL or PostgreSQL
func retrievePassword(
	passwordOrConnStr string,
) string {
	// Replace all spaces with semicolons to handle "key1=value1 key2=value2 ..." format
	password := strings.ReplaceAll(passwordOrConnStr, " ", ";")

	// Looking for the value of "password" key
	parts := strings.Split(password, ";")
	for _, part := range parts {
		// Trim spaces and make the comparison case-insensitive
		keyValue := strings.Split(strings.TrimSpace(part), "=")
		if len(keyValue) == 2 && strings.EqualFold(keyValue[0], "password") {
			// Return the part after "password="
			return keyValue[1]
		}
	}

	// If the password key was not found, return the connection string as the secret
	return password
}

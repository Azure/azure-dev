// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// MonitoringProperties requires a successfully decoded policy object.
func MonitoringProperties(resource *armresources.GenericResource) (map[string]any, error) {
	properties, ok := resource.Properties.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("monitoring resource policy is unavailable")
	}
	return properties, nil
}

// MonitoringKeyPolicy rejects policy changes needed to enable ApiKey ingestion.
func MonitoringKeyPolicy(properties map[string]any) error {
	if value, present := properties["DisableLocalAuth"]; present {
		disabled, ok := value.(bool)
		if !ok || disabled {
			return fmt.Errorf("automatic ApiKey monitoring cannot change or bypass disabled local authentication")
		}
	}
	if features, present := properties["features"]; present {
		values, ok := features.(map[string]any)
		if !ok {
			return fmt.Errorf("monitoring workspace authentication policy is unavailable")
		}
		if disabled, present := values["disableLocalAuth"]; present && disabled != false {
			return fmt.Errorf("automatic ApiKey monitoring cannot change or bypass disabled workspace local authentication")
		}
	}
	return MonitoringNetworkPolicy(properties)
}

// MonitoringNetworkPolicy rejects unsupported private/restricted ingestion.
func MonitoringNetworkPolicy(properties map[string]any) error {
	for _, name := range []string{"publicNetworkAccessForIngestion", "publicNetworkAccess"} {
		if value, present := properties[name]; present {
			access, ok := value.(string)
			if !ok || !strings.EqualFold(access, "Enabled") {
				return fmt.Errorf("automatic monitoring cannot change or bypass restricted public ingestion")
			}
		}
	}
	if acls, present := properties["networkAcls"]; present {
		values, ok := acls.(map[string]any)
		if !ok {
			return fmt.Errorf("monitoring network policy cannot be resolved")
		}
		if action, present := values["defaultAction"]; present && action != "Allow" {
			return fmt.Errorf("automatic monitoring does not configure private-network ingestion")
		}
	}
	return nil
}

// validateReusableAppInsightsConnection reports whether a connection is an
// AppInsights connection and, if so, whether its target and authentication can
// be reused automatically.
func validateReusableAppInsightsConnection(
	name, reserved string, properties *armcognitiveservices.ConnectionPropertiesV2,
) (isMonitoring bool, err error) {
	if properties == nil || properties.Category == nil {
		return false, fmt.Errorf("connection properties are unavailable; monitoring discovery is incomplete")
	}
	isMonitoring = strings.EqualFold(string(*properties.Category), "AppInsights")
	if strings.EqualFold(name, reserved) && !isMonitoring {
		return false, fmt.Errorf("the system monitoring connection name is already in use; no connection was changed")
	}
	if !isMonitoring {
		return false, nil
	}
	if properties.Target == nil || properties.AuthType == nil {
		return true, fmt.Errorf("existing AppInsights connection lacks its target or authentication type")
	}
	if _, err := MonitoringResourceID(*properties.Target, "Microsoft.Insights/components"); err != nil {
		return true, err
	}
	if resourceID := properties.Metadata["ResourceId"]; resourceID != nil &&
		!strings.EqualFold(*resourceID, *properties.Target) {
		return true, fmt.Errorf("existing AppInsights target and resource metadata disagree")
	}
	auth := string(*properties.AuthType)
	if !strings.EqualFold(auth, "ApiKey") && !strings.EqualFold(auth, "AAD") &&
		!strings.EqualFold(auth, "ManagedIdentity") {
		return true, fmt.Errorf("existing AppInsights authentication cannot be reused automatically")
	}
	return true, nil
}

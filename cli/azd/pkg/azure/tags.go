// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

const (
	// TagKeyAzdEnvName is the name of the key in the tags map of a resource
	// used to store the azd environment a resource is associated with.
	TagKeyAzdEnvName = "azd-env-name"
	// TagKeyAzdLayerName is the name of the key in the tags map of a resource
	// used to store the azd provisioning layer a resource is associated with.
	TagKeyAzdLayerName = "azd-layer-name"
	/* #nosec G101 - Potential hardcoded credentials - false positive */
	// TagKeyAzdDeploymentStateParamHashName is the name of the key in the tags map of a deployment
	// used to store the parameters hash.
	TagKeyAzdDeploymentStateParamHashName = "azd-provision-param-hash"
	// Used for stack deployments
	TagKeyAzdDeploymentTemplateHashName = "azd-provision-template-hash"
	// TagKeyAzdServiceName is the name of the key in the tags map of a resource
	// used to store the azd service a resource is associated with.
	TagKeyAzdServiceName = "azd-service-name"
)

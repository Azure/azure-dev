// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import "github.com/azure/azure-dev/cli/azd/pkg/osutil"

// DeploymentStacksConfig is the typed representation of the `infra.deploymentStacks`
// block in azure.yaml. It configures the Azure Deployment Stacks control-plane request
// (not the IaC template inputs) and is only valid when the provider is Bicep.
//
// The JSON tags intentionally match the camelCase keys expected by the
// armdeploymentstacks SDK (via its custom JSON marshaling) so the resolved
// configuration can be handed to the deployment-stacks API layer unchanged.
type DeploymentStacksConfig struct {
	// ActionOnUnmanage defines the behavior of resources that are no longer managed
	// after the deployment stack is updated or deleted.
	ActionOnUnmanage *ActionOnUnmanageConfig `yaml:"actionOnUnmanage,omitempty" json:"actionOnUnmanage,omitempty"`
	// DenySettings defines how resources deployed by the stack are locked.
	DenySettings *DenySettingsConfig `yaml:"denySettings,omitempty" json:"denySettings,omitempty"`
}

// ActionOnUnmanageConfig defines the unmanage behavior for each resource scope.
// Valid values are "delete" and "detach".
type ActionOnUnmanageConfig struct {
	Resources        string `yaml:"resources,omitempty" json:"resources,omitempty"`
	ResourceGroups   string `yaml:"resourceGroups,omitempty" json:"resourceGroups,omitempty"`
	ManagementGroups string `yaml:"managementGroups,omitempty" json:"managementGroups,omitempty"`
}

// DenySettingsConfig defines the deny-assignment (lock) configuration for a deployment stack.
//
// ExcludedActions and ExcludedPrincipals support ${VAR} environment-variable substitution,
// resolved from the azd environment at provision time. This makes per-environment values
// (for example, pipeline service principal or operator object IDs) expressible in a portable
// template instead of being committed as literals.
type DenySettingsConfig struct {
	// Mode defines the denied actions. One of "none", "denyDelete", "denyWriteAndDelete".
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`
	// ApplyToChildScopes applies the deny settings to child resource scopes of every managed
	// resource with a deny assignment.
	ApplyToChildScopes *bool `yaml:"applyToChildScopes,omitempty" json:"applyToChildScopes,omitempty"`
	// ExcludedActions is the list of role-based management operations excluded from the deny settings.
	ExcludedActions []osutil.ExpandableString `yaml:"excludedActions,omitempty" json:"excludedActions,omitempty"`
	// ExcludedPrincipals is the list of Entra ID principal IDs excluded from the lock.
	ExcludedPrincipals []osutil.ExpandableString `yaml:"excludedPrincipals,omitempty" json:"excludedPrincipals,omitempty"`
}

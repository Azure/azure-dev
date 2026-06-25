// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

// FoundryProviderName is the value written to `infra.provider` in
// azure.yaml by `azd ai agent init` and looked up by azd's provider
// resolver to dispatch provisioning to this extension.
const FoundryProviderName = "microsoft.foundry"

// BicepProviderName and TerraformProviderName are azd-core's built-in
// provisioning providers. `azd ai agent init --infra=terraform` stamps
// TerraformProviderName onto azure.yaml so azd-core's Terraform provider
// (not this extension's microsoft.foundry provider) handles provisioning.
const (
	BicepProviderName     = "bicep"
	TerraformProviderName = "terraform"
)

// FoundryProjectHost is the `services.<name>.host` value whose service body
// owns Foundry account/project provisioning inputs such as endpoint:, deployments:, and network:.
const FoundryProjectHost = "azure.ai.project"

// FoundryProjectServiceHosts lists the values that the provisioning provider
// treats as Foundry project services. Keep this project-scoped: agent services
// depend on the project service, but do not own account-level provisioning settings.
var FoundryProjectServiceHosts = []string{FoundryProjectHost}

// IsFoundryNetworkHost reports whether a host belongs to a Foundry service shape
// where network: would be a likely user mistake. The shipped network contract is
// project-scoped, so callers use this to reject misplaced network: blocks with
// actionable guidance instead of silently ignoring them.
func IsFoundryNetworkHost(host string) bool {
	return host == "azure.ai.agent" || host == "microsoft.foundry"
}

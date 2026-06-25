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

// FoundryServiceHosts lists the values of `services.<name>.host` that this
// extension's provisioning provider treats as Foundry services. Keep
// "azure.ai.agent" first so suggestions point users at the unified host while
// "microsoft.foundry" remains accepted for existing projects during migration.
// Must stay in sync with cmd.AiAgentHost ("azure.ai.agent") — kept here to avoid
// a cmd -> project import cycle.
var FoundryServiceHosts = []string{"azure.ai.agent", "microsoft.foundry"}

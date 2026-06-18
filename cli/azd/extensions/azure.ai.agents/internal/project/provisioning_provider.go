// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

// FoundryProviderName is the value written to `infra.provider` in
// azure.yaml by `azd ai agent init` and looked up by azd's provider
// resolver to dispatch provisioning to this extension.
const FoundryProviderName = "microsoft.foundry"

// FoundryServiceHosts lists the values of `services.<name>.host` that
// this extension's provisioning provider treats as Foundry services.
// Must stay in sync with cmd.AiAgentHost ("azure.ai.agent") — kept here
// to avoid a cmd -> project import cycle.
var FoundryServiceHosts = []string{"azure.ai.agent"}

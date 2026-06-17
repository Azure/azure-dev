// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

// FoundryProviderName is the value written to `infra.provider` in
// azure.yaml by `azd ai agent init` and looked up by azd's provider
// resolver to dispatch provisioning to this extension.
const FoundryProviderName = "microsoft.foundry"

// FoundryServiceHosts lists the values of `services.<name>.host` that
// this extension's provisioning provider treats as Foundry services.
// FoundryHost ("microsoft.foundry") is the unified host emitted by
// `azd ai agent init`; "azure.ai.agent" is the legacy host kept for
// backward compatibility. The literal mirrors cmd.AiAgentHost — kept
// here to avoid a cmd -> project import cycle.
var FoundryServiceHosts = []string{FoundryHost, "azure.ai.agent"}

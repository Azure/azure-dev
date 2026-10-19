// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package envkeys defines typed environment keys for Foundry ACR.
//
// Registered keys:
//   - AZURE_CONTAINER_REGISTRY_RESOURCE_ID: Input, registry ID.
//   - AZURE_CONTAINER_REGISTRY_ENDPOINT: Output, the login server.
//   - AZD_FOUNDRY_ACR_MODE: the validated ACR provisioning mode.
//   - AZURE_AI_PROJECT_ACR_CONNECTION_NAME: Output, connection name.
//   - AZD_FOUNDRY_ACR_PULL_ASSIGNED: Output, "true" or "false".
//   - AZD_AGENT_SKIP_ACR: Input, the user/CI skip flag.
//
// Input has no Set method here, not a global write restriction.
// Init records explicit choices through existing environment helpers.
// Provisioning outputs and cleanup retain existing behavior.
// ParseAcrMode validates values; callers own defaults and skipping.
package envkeys

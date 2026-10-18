// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package envkeys registers typed azd environment keys for Foundry ACR flows.
//
// Registered keys:
//   - AZURE_CONTAINER_REGISTRY_RESOURCE_ID: Input, the user-selected registry.
//   - AZURE_CONTAINER_REGISTRY_ENDPOINT: Output, the registry login server.
//   - AZD_FOUNDRY_ACR_MODE: Enum[AcrMode], create, reuse-connect, already-connected, or none.
//   - AZURE_AI_PROJECT_ACR_CONNECTION_NAME: Output, the project registry connection.
//   - AZD_FOUNDRY_ACR_PULL_ASSIGNED: Output, pull assignment state ("true"/"false").
//   - AZD_AGENT_SKIP_ACR: Input, the user/CI skip flag.
//
// Input deliberately has no Set method. Init records user choices using Name()
// with its existing environment helpers. JSON supports future structured keys;
// no deployment JSON key is registered in this first wave.
//
// Run ../scripts/check_envkey_literals.sh from the extension directory to check
// the raw-literal ratchet. Its --counts option prints baseline-format counts;
// lower the checked-in baseline as subsequent migrations remove literals.
package envkeys

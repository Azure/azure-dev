// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azureaiagent/internal/synthesis"
)

// environmentReference and findEnvironmentReferences are this package's view of
// the single azd ${VAR} scanner. See [synthesis.FindEnvReferences] for the
// discovery rules; the policy layered on top lives in the callers
// (collectAzureYamlEnvironmentReferences for init prompting,
// collectStringEnvironmentTemplates for the generated service env block).
//
// The implementation lives in internal/synthesis because that package is the
// other consumer — resolveVars derives its unresolved-variable guard from the
// same scan — and its two byte-identical copies (this extension and
// azure.ai.projects) can only share code through an import path both spell
// identically. pkg/foundry, next to ExpandEnv, is the natural home, but both
// extensions consume azd core at a pinned release, so moving it there needs a
// core release plus a go.mod bump in both modules. Tracked by
// https://github.com/Azure/azure-dev/issues/9427.
//
// Escape handling is no longer a per-field choice. Every Foundry field,
// including the three project network values (network.agentSubnet.vnet,
// network.peSubnet.vnet, network.dns.subscription), resolves through
// foundry.ExpandEnv, which collapses '$' pairs so $${VAR} stays literal and
// reserves ${{...}} spans for Foundry.
type environmentReference = synthesis.EnvReference

func findEnvironmentReferences(value string) []environmentReference {
	return synthesis.FindEnvReferences(value)
}

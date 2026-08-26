// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"fmt"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_api"
)

// PromptFeature names an agent.yaml capability whose availability depends on
// the execution harness the prompt agent runs on.
//
// These are *capability* names, matching how the Foundry portal groups them.
// None of them is a field on the prompt-agent API — each is carried by an
// existing primitive, which is what declares() inspects:
//
//	memory     -> a memory store resource + a memory_search_preview tool entry
//	guardrails -> policies: (rai_policy) -> the definition's rai_config
//	knowledge  -> grounding tools (file_search, azure_ai_search, bing_grounding,
//	              sharepoint_grounding_preview, ...), including the file_search
//	              entry azd synthesizes from the vector-assets/ folder
type PromptFeature string

const (
	// PromptFeatureMemory is the `memory:` block — durable recall carried
	// across invocations, backed by a Foundry memory store.
	PromptFeatureMemory PromptFeature = "memory"

	// PromptFeatureGuardrails is the `policies:` block — safety and governance
	// constraints applied to the agent's inputs and outputs.
	PromptFeatureGuardrails PromptFeature = "guardrails"

	// PromptFeatureKnowledge is grounding: vector-assets/ plus any retrieval
	// tool the agent declares.
	PromptFeatureKnowledge PromptFeature = "knowledge"
)

// promptFeatureOrder fixes the order features are reported in. Map iteration
// order is randomized, so errors built from harnessedPromptFeatures alone would
// name the same fields in a different order on each run.
var promptFeatureOrder = []PromptFeature{
	PromptFeatureMemory,
	PromptFeatureGuardrails,
	PromptFeatureKnowledge,
}

// knowledgeToolTypes are the tool `type` values that ground an agent in an
// external corpus. This list only classifies a declared tool as "knowledge" for
// the harness gate below — it is NOT an allowlist. Tools are passed through to
// the API verbatim, so a type missing from this list still deploys; it simply
// is not counted as knowledge.
var knowledgeToolTypes = map[string]bool{
	"file_search":                  true,
	"azure_ai_search":              true,
	"bing_grounding":               true,
	"bing_custom_search_preview":   true,
	"sharepoint_grounding_preview": true,
	"fabric_dataagent_preview":     true,
	"fabric_iq_preview":            true,
	"work_iq_preview":              true,
}

// harnessedPromptFeatures records whether each capability is honored by a
// *harnessed* prompt agent — a managed agent that names a harness such as
// "github-copilot" and runs in a platform-provisioned sandbox.
//
// This map is the switch, and it follows the harness spec literally: a
// capability is enabled only where the spec says the harness honors it.
//
//   - guardrails: enabled. The spec documents RAI policy attachment for
//     harnessed agents.
//   - knowledge: disabled. The spec puts grounding explicitly out of scope for
//     the harness, which owns its own retrieval.
//   - memory: disabled. The spec never describes memory for a harnessed agent,
//     and the harness sandbox has no memory store to bind to.
//
// A disabled capability makes deploy fail fast with an actionable message
// naming the field, instead of silently dropping it after a successful-looking
// deploy. Flip an entry back to true once Foundry confirms the harness honors
// it. Harness-less prompt agents are unaffected in either state.
var harnessedPromptFeatures = map[PromptFeature]bool{
	PromptFeatureMemory:     false,
	PromptFeatureGuardrails: true,
	PromptFeatureKnowledge:  false,
}

// declares reports whether the agent configures the given capability, by
// inspecting the primitive that actually carries it.
func (p PromptAgent) declares(feature PromptFeature) bool {
	switch feature {
	case PromptFeatureMemory:
		return p.Memory != nil
	case PromptFeatureGuardrails:
		for _, policy := range p.Policies {
			if policy.Type == PolicyTypeRai && strings.TrimSpace(policy.RaiPolicyName) != "" {
				return true
			}
		}
		return false
	case PromptFeatureKnowledge:
		for _, raw := range p.Tools {
			tool, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if knowledgeToolTypes[fmt.Sprintf("%v", tool["type"])] {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// UnsupportedHarnessFeatures lists the capabilities this agent configures that
// its harness cannot honor, in a stable order. It returns nil for a harness-less
// prompt agent, which supports all of them.
func (p PromptAgent) UnsupportedHarnessFeatures() []PromptFeature {
	if !p.harnessed() {
		return nil
	}

	var unsupported []PromptFeature
	for _, feature := range promptFeatureOrder {
		if harnessedPromptFeatures[feature] {
			continue
		}
		if p.declares(feature) {
			unsupported = append(unsupported, feature)
		}
	}
	return unsupported
}

// ValidateHarnessFeatures rejects capabilities the agent's harness cannot
// honor. Failing loudly is deliberate: the alternative is publishing an agent
// that looks correctly configured but ignores the capability at runtime, which
// is far harder to diagnose than a deploy-time error.
func (p PromptAgent) ValidateHarnessFeatures() error {
	unsupported := p.UnsupportedHarnessFeatures()
	if len(unsupported) == 0 {
		return nil
	}

	names := make([]string, 0, len(unsupported))
	for _, feature := range unsupported {
		names = append(names, string(feature))
	}

	return fmt.Errorf(
		"agent.yaml configures %s, which the %q harness does not support yet",
		strings.Join(names, ", "), p.HarnessType(),
	)
}

// raiPolicyIDPrefix and raiPolicyIDSegment are the two fixed parts of a RAI
// policy's ARM resource ID.
const (
	raiPolicyIDPrefix  = "/subscriptions/"
	raiPolicyIDSegment = "/raiPolicies/"
)

// ValidateRaiPolicyName rejects a policy value that is not a full ARM resource
// ID.
//
// The service reports a bare policy name as "invalid or does not exist", which
// reads like a missing resource and sends authors hunting for a policy that is
// in fact present on their account. The real cause is the shape of the value,
// so name that instead of letting the deploy fail on a misleading message.
func ValidateRaiPolicyName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, raiPolicyIDPrefix) && strings.Contains(trimmed, raiPolicyIDSegment) {
		return nil
	}
	return fmt.Errorf(
		"rai_policy_name %q must be the policy's full ARM resource ID, not its short name; "+
			"expected the form /subscriptions/<sub>/resourceGroups/<rg>/providers/"+
			"Microsoft.CognitiveServices/accounts/<account>/raiPolicies/<policy>",
		trimmed,
	)
}

// ValidatePolicies rejects policy entries the service will refuse.
func (p PromptAgent) ValidatePolicies() error {
	for i, policy := range p.Policies {
		if policy.Type != PolicyTypeRai {
			continue
		}
		if err := ValidateRaiPolicyName(policy.RaiPolicyName); err != nil {
			return fmt.Errorf("policies[%d]: %w", i, err)
		}
	}
	return nil
}

// removedHarnesses maps a harness spelling that is no longer accepted to the
// spelling that replaced it.
//
// Unlike tool types, an unrecognized harness is *not* rejected: a harness azd
// has never heard of may simply be newer than this build, and hard-failing
// would make every new Foundry harness a breaking change in azd. Only spellings
// known to be wrong are refused, and each one names its replacement.
var removedHarnesses = agent_api.RemovedManagedAgentHarnesses

// ValidateHarness rejects harness spellings that have been replaced. The value
// is passed to the service verbatim, and the service ignores a harness it does
// not recognize rather than erroring — so an outdated spelling would otherwise
// publish a plain prompt agent while the manifest claims a managed one.
func (p PromptAgent) ValidateHarness() error {
	harness := p.HarnessType()
	if harness == "" {
		return nil
	}
	if replacement, removed := removedHarnesses[harness]; removed {
		return fmt.Errorf(
			"harness %q is no longer accepted; use %q instead",
			harness, replacement,
		)
	}
	return nil
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_api"
)

// The gates in this file apply only to the github_copilot_preview harness. A
// plain prompt agent or unknown future harness keeps every field and tool type
// it accepts today.
//
// These are rejections rather than warnings because the spec is explicit that
// the service does not silently ignore them: a manifest carrying one of these
// fields fails at the API. Catching it here turns a late, opaque service error
// into a deploy-time message that names the offending key.

// harnessRejectedToolTypes are tool `type` values a harnessed prompt agent may
// not declare.
//
// A harness runs its tools through a platform-managed toolbox, and these types
// have no toolbox representation: they either need a customer-supplied
// execution target the sandbox does not expose (function, azure_function,
// custom, openapi-adjacent shells) or duplicate a capability the harness
// already provides natively (shell, local_shell, computer, apply_patch).
//
// Unlike knownPromptToolTypes this *is* an authoritative list, taken from the
// spec's rejection table. A type absent from it is allowed through, so tool
// types newer than this build still deploy.
var harnessRejectedToolTypes = map[string]struct{}{
	"apply_patch":                {},
	"azure_function":             {},
	"bing_grounding":             {},
	"capture_structured_outputs": {},
	"computer":                   {},
	"custom":                     {},
	"function":                   {},
	"image_generation":           {},
	"local_shell":                {},
	"namespace":                  {},
	"programmatic_tool_calling":  {},
	"shell":                      {},
}

// reasoningEffortKey is the single `reasoning` property a harness honors.
const reasoningEffortKey = "effort"

// harnessBuiltInCapabilities are the built-in capability groups a harnessed
// agent may allow or exclude, in the order they are reported.
//
// Unlike tool types, this list *is* closed: `builtin_tools` filters a fixed set
// the harness provides, so a name outside it can only be a typo, and silently
// dropping it would leave the author believing they had turned a capability off.
var harnessBuiltInCapabilities = []string{
	"filesystem_read",
	"filesystem_write",
	"shell",
	"subagents",
	"web",
}

// NewPromptHarness returns a harness block naming only its type, which is the
// whole of the block for an agent that takes the harness defaults. It returns
// nil for an empty type so callers can pass an unresolved harness straight
// through and get a plain prompt agent.
func NewPromptHarness(harnessType string) *PromptHarness {
	harnessType = strings.TrimSpace(harnessType)
	if harnessType == "" {
		return nil
	}
	return &PromptHarness{Type: harnessType}
}

// HarnessType returns the harness discriminator the agent runs on, or "" for a
// plain prompt agent with no harness.
func (p PromptAgent) HarnessType() string {
	if p.Harness == nil {
		return ""
	}
	return strings.TrimSpace(p.Harness.Type)
}

// harnessed reports whether the agent names an execution harness.
func (p PromptAgent) harnessed() bool {
	return p.HarnessType() != ""
}

// ValidateHarnessBlock rejects a malformed `harness:` block.
//
// Each rule mirrors one the service enforces, so failing here turns an opaque
// API rejection into a message that names the offending key.
func (p PromptAgent) ValidateHarnessBlock() error {
	if p.Harness == nil {
		return nil
	}
	if p.HarnessType() == "" {
		return fmt.Errorf(
			"agent.yaml declares a harness with no type; set harness.type (for example %q), "+
				"or remove the harness block to run as a plain prompt agent",
			agent_api.ManagedAgentHarnessGitHubCopilot,
		)
	}
	if p.HarnessType() != agent_api.ManagedAgentHarnessGitHubCopilot {
		return nil
	}
	if err := p.validateHarnessEnvironment(); err != nil {
		return err
	}
	return p.validateHarnessBuiltInTools()
}

// validateHarnessEnvironment rejects a half-specified sandbox size.
//
// cpu and memory are a pair: the service refuses one without the other rather
// than defaulting the missing half, so a manifest setting only `cpu` would fail
// at deploy with no indication that `memory` is what is missing.
func (p PromptAgent) validateHarnessEnvironment() error {
	env := p.Harness.Environment
	if env == nil {
		return nil
	}
	cpu := strings.TrimSpace(env.Cpu)
	memory := strings.TrimSpace(env.Memory)
	if (cpu == "") == (memory == "") {
		return nil
	}

	set, missing := "cpu", "memory"
	if cpu == "" {
		set, missing = "memory", "cpu"
	}
	return fmt.Errorf(
		"agent.yaml sets harness.environment.%s without harness.environment.%s; "+
			"the %q harness sizes the sandbox from both, so set them together or set neither",
		set, missing, p.HarnessType(),
	)
}

// validateHarnessBuiltInTools rejects capability names the harness does not
// define, in either the allowed or the excluded list.
func (p PromptAgent) validateHarnessBuiltInTools() error {
	builtin := p.Harness.BuiltinTools
	if builtin == nil {
		return nil
	}

	var unknown []string
	check := func(field string, names *[]string) {
		if names == nil {
			return
		}
		for _, name := range *names {
			name = strings.TrimSpace(name)
			if slices.Contains(harnessBuiltInCapabilities, name) {
				continue
			}
			entry := fmt.Sprintf("%s.%s", field, name)
			if !slices.Contains(unknown, entry) {
				unknown = append(unknown, entry)
			}
		}
	}
	check("allowed", builtin.Allowed)
	check("excluded", builtin.Excluded)

	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)

	return fmt.Errorf(
		"agent.yaml lists harness.builtin_tools.%s, which the %q harness does not define; "+
			"supported capabilities are %s",
		strings.Join(unknown, ", harness.builtin_tools."),
		p.HarnessType(),
		strings.Join(harnessBuiltInCapabilities, ", "),
	)
}

// ValidateHarnessFields rejects sampling and output-shaping fields a harnessed
// prompt agent may not set.
//
// The harness owns decoding: it supplies its own sampling parameters and its
// own response format, so an author-supplied temperature, top_p, tool_choice or
// text block would be overridden rather than applied.
func (p PromptAgent) ValidateHarnessFields() error {
	if p.HarnessType() != agent_api.ManagedAgentHarnessGitHubCopilot {
		return nil
	}

	var rejected []string
	if p.Temperature != nil {
		rejected = append(rejected, "temperature")
	}
	if p.TopP != nil {
		rejected = append(rejected, "top_p")
	}
	if p.ToolChoice != nil {
		rejected = append(rejected, "tool_choice")
	}
	if p.Text != nil {
		rejected = append(rejected, "text")
	}

	if len(rejected) > 0 {
		return fmt.Errorf(
			"agent.yaml sets %s, which the %q harness does not accept because it controls "+
				"sampling and response format itself",
			strings.Join(rejected, ", "), p.HarnessType(),
		)
	}

	return p.validateHarnessReasoning()
}

// validateHarnessReasoning rejects `reasoning` properties other than `effort`.
//
// A non-mapping `reasoning` value is left alone: it is either absent or already
// malformed, and reporting a shape error here would mask the clearer one the
// schema check produces.
func (p PromptAgent) validateHarnessReasoning() error {
	reasoning, ok := p.Reasoning.(map[string]any)
	if !ok {
		return nil
	}

	var extra []string
	for key := range reasoning {
		if key != reasoningEffortKey {
			extra = append(extra, key)
		}
	}
	if len(extra) == 0 {
		return nil
	}
	sort.Strings(extra)

	return fmt.Errorf(
		"agent.yaml sets reasoning.%s, which the %q harness does not accept; "+
			"only reasoning.%s is supported",
		strings.Join(extra, ", reasoning."), p.HarnessType(), reasoningEffortKey,
	)
}

// ValidateHarnessTools rejects declared tool types a harnessed prompt agent
// cannot run. Structurally malformed entries are skipped — ValidateTools
// reports those, with a better message.
func (p PromptAgent) ValidateHarnessTools() error {
	if p.HarnessType() != agent_api.ManagedAgentHarnessGitHubCopilot {
		return nil
	}

	var rejected []string
	for _, raw := range p.Tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		toolType, err := toolTypeOf(tool)
		if err != nil {
			continue
		}
		if _, bad := harnessRejectedToolTypes[toolType]; !bad {
			continue
		}
		if !slices.Contains(rejected, toolType) {
			rejected = append(rejected, toolType)
		}
	}

	if len(rejected) == 0 {
		return nil
	}
	sort.Strings(rejected)

	return fmt.Errorf(
		"agent.yaml declares tool %s, which the %q harness does not accept because it runs "+
			"tools through a platform-managed toolbox",
		strings.Join(rejected, ", "), p.HarnessType(),
	)
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// The gates in this file apply *only* to harnessed prompt agents. A harness-less
// prompt agent keeps every field and tool type it accepts today — the harness
// spec constrains the sandboxed execution environment, not the base agent.
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

// harnessed reports whether the agent names an execution harness.
func (p PromptAgent) harnessed() bool {
	return strings.TrimSpace(p.Harness) != ""
}

// ValidateHarnessFields rejects sampling and output-shaping fields a harnessed
// prompt agent may not set.
//
// The harness owns decoding: it supplies its own sampling parameters and its
// own response format, so an author-supplied temperature, top_p, tool_choice or
// text block would be overridden rather than applied.
func (p PromptAgent) ValidateHarnessFields() error {
	if !p.harnessed() {
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
			strings.Join(rejected, ", "), strings.TrimSpace(p.Harness),
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
		strings.Join(extra, ", reasoning."), strings.TrimSpace(p.Harness), reasoningEffortKey,
	)
}

// ValidateHarnessTools rejects declared tool types a harnessed prompt agent
// cannot run. Structurally malformed entries are skipped — ValidateTools
// reports those, with a better message.
func (p PromptAgent) ValidateHarnessTools() error {
	if !p.harnessed() {
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
		strings.Join(rejected, ", "), strings.TrimSpace(p.Harness),
	)
}

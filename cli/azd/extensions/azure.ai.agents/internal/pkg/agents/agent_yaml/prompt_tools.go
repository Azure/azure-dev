// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// knownPromptToolTypes is the set of tool `type` discriminators the Foundry
// prompt-agent API defines, mirroring the service's ToolType enum.
//
// This is a **recognition list, not an allowlist**. `tools:` is passed through
// verbatim precisely so authors can use a tool type that ships before azd knows
// about it, and hard-failing on an unrecognized type would make every new
// service tool a breaking change in azd. An unrecognized type is therefore
// reported as a warning and still deployed.
//
// It exists because the failure mode without it is the worst kind: the service
// ignores tool entries whose type it does not recognize, so a typo deploys
// "successfully" and produces an agent that silently lacks the capability.
var knownPromptToolTypes = map[string]struct{}{
	"a2a_preview":                  {},
	"apply_patch":                  {},
	"azure_ai_search":              {},
	"azure_function":               {},
	"bing_custom_search_preview":   {},
	"bing_grounding":               {},
	"browser_automation_preview":   {},
	"capture_structured_outputs":   {},
	"code_interpreter":             {},
	"computer":                     {},
	"computer_use_preview":         {},
	"custom":                       {},
	"fabric_dataagent_preview":     {},
	"fabric_iq_preview":            {},
	"file_search":                  {},
	"function":                     {},
	"image_generation":             {},
	"local_shell":                  {},
	"mcp":                          {},
	"memory_search_preview":        {},
	"namespace":                    {},
	"openapi":                      {},
	"reminder_preview":             {},
	"shell":                        {},
	"sharepoint_grounding_preview": {},
	"tool_search":                  {},
	"toolbox_search":               {},
	"toolbox_search_preview":       {},
	"web_iq_preview":               {},
	"web_search":                   {},
	"web_search_preview":           {},
	"work_iq_preview":              {},
}

// removedPromptToolTypes maps tool types the API used to define onto the type
// that replaced them. These are called out separately from merely-unrecognized
// types because the author almost certainly meant the replacement, and because
// the two spellings are close enough to be mistaken for each other.
var removedPromptToolTypes = map[string]string{
	"memory_search": "memory_search_preview",
}

// ValidateTools rejects entries in `tools:` that are structurally malformed.
//
// Only unambiguous errors are raised here: an entry that is not a mapping, or
// one with no usable `type`. Both are inert on the wire — the service cannot
// dispatch a tool it cannot identify — so accepting them would publish an agent
// missing a capability its manifest claims. Unrecognized (as opposed to
// missing) types are deliberately not an error; see UnrecognizedToolTypes.
func (p *PromptAgent) ValidateTools() error {
	for i, raw := range p.Tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf(
				"tools[%d] must be a mapping with a 'type' key, got %T", i, raw)
		}

		toolType, err := toolTypeOf(tool)
		if err != nil {
			return fmt.Errorf("tools[%d]: %w", i, err)
		}

		if replacement, removed := removedPromptToolTypes[toolType]; removed {
			return fmt.Errorf(
				"tools[%d] uses tool type %q, which the API no longer defines; use %q instead",
				i, toolType, replacement)
		}
	}
	return nil
}

// UnrecognizedToolTypes returns the declared tool types azd does not recognize,
// sorted and deduplicated. Callers surface these as warnings during deploy.
//
// A non-empty result is usually a typo, but may equally be a tool type newer
// than this build of azd — which is why it does not fail the deploy.
func (p *PromptAgent) UnrecognizedToolTypes() []string {
	var unrecognized []string

	for _, raw := range p.Tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		toolType, err := toolTypeOf(tool)
		if err != nil {
			continue
		}
		if _, known := knownPromptToolTypes[toolType]; known {
			continue
		}
		if !slices.Contains(unrecognized, toolType) {
			unrecognized = append(unrecognized, toolType)
		}
	}

	sort.Strings(unrecognized)
	return unrecognized
}

// toolTypeOf extracts the `type` discriminator from a decoded tool entry.
func toolTypeOf(tool map[string]any) (string, error) {
	raw, present := tool["type"]
	if !present {
		return "", fmt.Errorf("tool entry is missing a 'type' key")
	}

	// YAML decodes an unquoted scalar to its natural Go type, so a mistake like
	// `type: 42` arrives as an int rather than a string. Reject it by shape
	// instead of stringifying, which would turn the mistake into a plausible
	// looking tool type.
	toolType, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("tool 'type' must be a string, got %T", raw)
	}

	toolType = strings.TrimSpace(toolType)
	if toolType == "" {
		return "", fmt.Errorf("tool 'type' must not be empty")
	}

	return toolType, nil
}

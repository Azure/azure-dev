// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// A prompt agent's definition reaches azd by one of two routes, and only one of
// them runs the YAML decoder:
//
//   - Inline on the azure.yaml service entry. Core azd parses azure.yaml, hands
//     the service properties to the extension as protobuf, and the extension
//     decodes them as JSON. The UnmarshalYAML methods in yaml.go never run.
//   - From a file named by `$ref:` (or the legacy agent.yaml convention), which
//     the deploy path reads and decodes as YAML.
//
// Inline is the shape `azd ai agent init` scaffolds, so without the checks below
// the common case would be the unchecked one: a `harness:` typo would silently
// bind nothing and deploy an agent with capabilities the author believed they
// had turned off. These functions apply the same rules to a decoded value that
// [PromptHarness.UnmarshalYAML] and [PromptMemory.UnmarshalYAML] apply to a
// yaml.Node, so both routes reject the same manifests with the same messages.

// errHarnessStringForm reports the pre-block `harness: <string>` spelling,
// echoing the block that replaces it. An author carrying an older manifest
// forward is shown the replacement rather than a Go type name.
func errHarnessStringForm(value string) error {
	replacement := value
	if replacement == harnessTypeObsoleteAbbreviation {
		replacement = harnessTypeGitHubCopilotPreview
	}
	return fmt.Errorf(
		"harness must be a block, not a string: replace `harness: %s` with\n"+
			"  harness:\n"+
			"    type: %s",
		value, replacement)
}

// errHarnessObsoleteType reports the retired `ghcp` harness type by name so the
// value is not forwarded to a service that reports it as an opaque bad request.
func errHarnessObsoleteType() error {
	return fmt.Errorf(
		"harness.type %q is no longer accepted: use %q",
		harnessTypeObsoleteAbbreviation, harnessTypeGitHubCopilotPreview)
}

// ValidateInlinePromptAgent applies the authored-block rules to prompt-agent
// properties that were decoded outside this package, such as the inline
// definition carried on an azure.yaml service entry.
//
// props is the raw property bag. Keys the prompt agent forwards verbatim
// (tools, text, reasoning, structured_inputs) are deliberately not inspected so
// a tool type newer than this build still passes through.
func ValidateInlinePromptAgent(props map[string]any) error {
	if raw, ok := props["harness"]; ok {
		if err := validateInlineHarness(raw); err != nil {
			return err
		}
	}
	if raw, ok := props["memory"]; ok {
		if err := validateInlineMemory(raw); err != nil {
			return err
		}
	}
	return nil
}

// validateInlineHarness mirrors [PromptHarness.UnmarshalYAML].
func validateInlineHarness(value any) error {
	switch v := value.(type) {
	case nil:
		// An empty block leaves the zero value in place, matching decodeStrict.
		return nil
	case string:
		return errHarnessStringForm(v)
	case map[string]any:
		if declared, ok := v["type"].(string); ok && declared == harnessTypeObsoleteAbbreviation {
			return errHarnessObsoleteType()
		}
		// A distinct type so the YAML method is not inherited, matching the
		// decoder path.
		type harnessFields PromptHarness
		var decoded harnessFields
		if err := decodeStrictJSON(v, &decoded); err != nil {
			return fmt.Errorf("harness: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("harness must be a block with a `type:` key, got %s", inlineKindName(value))
	}
}

// validateInlineMemory mirrors [PromptMemory.UnmarshalYAML].
func validateInlineMemory(value any) error {
	if value == nil {
		return nil
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("memory must be a block with a `store:` key, got %s", inlineKindName(value))
	}
	// A distinct type so the YAML method is not inherited, matching the decoder
	// path.
	type memoryFields PromptMemory
	var decoded memoryFields
	if err := decodeStrictJSON(fields, &decoded); err != nil {
		return fmt.Errorf("memory: %w", err)
	}
	return nil
}

// decodeStrictJSON decodes value into out, rejecting keys that bind to no
// field. It is the JSON counterpart of decodeStrict.
func decodeStrictJSON(value any, out any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to re-encode: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(out)
}

// inlineKindName renders a decoded value's shape for an error message, so a
// reader sees "a list" rather than a Go type name. It is the counterpart of
// nodeKindName.
func inlineKindName(value any) string {
	switch value.(type) {
	case []any:
		return "a list"
	case string, bool, float64, int, int64:
		return "a value"
	case nil:
		return "an empty value"
	default:
		return "an unsupported value"
	}
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// A prompt agent's definition reaches azd through the effective azure.yaml
// service property map:
//
//   - Inline properties are decoded by core and handed to the extension as
//     protobuf.
//   - An explicit root `$ref:` is expanded into that same property map, with
//     service-level properties overlaid on the referenced values.
//
// The effective map is decoded as JSON, so the UnmarshalYAML methods in yaml.go
// never run. These functions apply the same strict authored-block rules before
// decoding so both source forms reject the same definitions.

var containerOnlyPromptFields = []string{
	"image",
	"protocols",
	"agentEndpoint",
	"agentCard",
	"codeConfiguration",
	"docker",
	"runtime",
	"startupCommand",
}

// ValidateInlinePromptAgent applies the authored-block rules to prompt-agent
// properties that were decoded outside this package, such as the inline
// definition carried on an azure.yaml service entry.
//
// props is the raw property bag. Keys the prompt agent forwards verbatim
// (tools, text, reasoning, structuredInputs) are deliberately not inspected so
// a tool type newer than this build still passes through.
func ValidateInlinePromptAgent(props map[string]any) error {
	for _, field := range containerOnlyPromptFields {
		if _, ok := props[field]; ok {
			return fmt.Errorf(
				"field %q is not valid for a prompt (kind: prompt) agent; "+
					"remove container-only fields (image, protocols, codeConfiguration, ...) "+
					"or use kind: hosted for container agents",
				field,
			)
		}
	}
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
		return fmt.Errorf("harness must be a block with a `type:` key, got string")
	case map[string]any:
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

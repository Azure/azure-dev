// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package evalcore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// BuiltinPrefix marks an evaluator provided by the platform. The prefix is
// stripped before the name is sent as testing_criteria[].evaluator_name.
const BuiltinPrefix = "builtin."

// EvaluatorRef references an evaluator from an eval. It accepts either a
// bare string or a mapping carrying the rest of the declaration:
//
//	evaluators:
//	  - builtin.task_adherence
//	  - name: support-quality
//	    source: ./evaluators/support-quality.json
//	    initialization_parameters:
//	      deployment_name: gpt-5.6-luna
//
// A built-in needs nothing but its name. One with a Source is the project's
// own, and is published before the eval that references it is created.
type EvaluatorRef struct {
	Name    string `yaml:"name" json:"name"`
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
	// Source names a local rubric JSON file. Empty means the evaluator is
	// already registered, or is a built-in.
	Source string `yaml:"source,omitempty" json:"source,omitempty"`
	// InitializationParameters are passed through to the testing criterion,
	// which is where a judge deployment is named.
	InitializationParameters map[string]any `yaml:"initialization_parameters,omitempty" json:"initialization_parameters,omitempty"`
	// Threshold maps to testing_criteria[].initialization_parameters.threshold.
	Threshold *float64 `yaml:"threshold,omitempty" json:"threshold,omitempty"`
}

// IsBuiltin reports whether the reference names a platform evaluator, which
// needs no declaration and is never uploaded.
func (e EvaluatorRef) IsBuiltin() bool {
	return strings.HasPrefix(e.Name, BuiltinPrefix)
}

// APIName is the name the service expects, with the builtin prefix removed.
func (e EvaluatorRef) APIName() string {
	return strings.TrimPrefix(e.Name, BuiltinPrefix)
}

// EvaluatorList is a sequence of EvaluatorRef supporting mixed string and
// mapping entries.
type EvaluatorList []EvaluatorRef

func (el *EvaluatorList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.SequenceNode {
		return fmt.Errorf("evaluators must be a sequence, got %v", value.Kind)
	}

	result := make([]EvaluatorRef, 0, len(value.Content))
	for _, node := range value.Content {
		switch node.Kind {
		case yaml.ScalarNode:
			var name string
			if err := node.Decode(&name); err != nil {
				return fmt.Errorf("decoding evaluator name: %w", err)
			}
			result = append(result, EvaluatorRef{Name: name})
		case yaml.MappingNode:
			var ref EvaluatorRef
			if err := node.Decode(&ref); err != nil {
				return fmt.Errorf("decoding evaluator: %w", err)
			}
			if ref.Name == "" {
				return fmt.Errorf("evaluator entry is missing 'name'")
			}
			result = append(result, ref)
		default:
			return fmt.Errorf("evaluator entry must be a string or a mapping, got %v", node.Kind)
		}
	}

	*el = result
	return nil
}

// MarshalYAML emits the compact string form when an entry carries nothing but a
// name, so round-tripping a hand-written config does not rewrite it.
func (el EvaluatorList) MarshalYAML() (any, error) {
	out := make([]any, 0, len(el))
	for _, ref := range el {
		if ref.isBareName() {
			out = append(out, ref.Name)
			continue
		}
		out = append(out, ref)
	}
	return out, nil
}

// UnmarshalJSON accepts the same mixed string-or-mapping form as the YAML
// decoder.
//
// This matters for the service-target provider: azd hands the service entry to
// the extension as JSON, so a config written as `- builtin.task_adherence`
// arrives as a bare string and would otherwise fail to decode.
func (el *EvaluatorList) UnmarshalJSON(data []byte) error {
	var entries []json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("evaluators must be a list: %w", err)
	}

	result := make([]EvaluatorRef, 0, len(entries))
	for _, entry := range entries {
		trimmed := bytes.TrimSpace(entry)
		if len(trimmed) > 0 && trimmed[0] == '"' {
			var name string
			if err := json.Unmarshal(trimmed, &name); err != nil {
				return fmt.Errorf("decoding evaluator name: %w", err)
			}
			result = append(result, EvaluatorRef{Name: name})
			continue
		}

		var ref EvaluatorRef
		if err := json.Unmarshal(trimmed, &ref); err != nil {
			return fmt.Errorf("decoding evaluator: %w", err)
		}
		if ref.Name == "" {
			return fmt.Errorf("evaluator entry is missing 'name'")
		}
		result = append(result, ref)
	}

	*el = result
	return nil
}

// MarshalJSON mirrors MarshalYAML's compact form.
//
// Everything the reference carries has to survive the round trip, including
// the source and the initialization parameters: the eval fingerprint is taken
// over this encoding, so a field dropped here is a change the reconciler
// cannot see.
func (el EvaluatorList) MarshalJSON() ([]byte, error) {
	// Aliased so the element encoder does not recurse through this method.
	type ref = EvaluatorRef

	out := make([]any, 0, len(el))
	for _, r := range el {
		if r.isBareName() {
			out = append(out, r.Name)
			continue
		}
		out = append(out, ref(r))
	}
	return json.Marshal(out)
}

// isBareName reports whether the reference carries nothing but its name, in
// which case both encoders emit the compact string form.
func (e EvaluatorRef) isBareName() bool {
	return e.Threshold == nil && e.Version == "" && e.Source == "" &&
		len(e.InitializationParameters) == 0
}

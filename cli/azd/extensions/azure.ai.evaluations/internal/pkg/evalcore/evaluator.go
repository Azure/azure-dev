// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package evalcore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"azureaieval/internal/messages"

	"go.yaml.in/yaml/v3"
)

// BuiltinPrefix marks an evaluator provided by the platform. The prefix is
// stripped before the name is sent as testing_criteria[].evaluator_name.
const BuiltinPrefix = "builtin."

// EvaluatorRef is one entry in an eval's `evaluators:` list. Every entry is a
// map keyed `evaluator:`; what to publish lives on the catalog entry instead,
// so a reference only names an evaluator and says how to run it:
//
//	evaluators:
//	  - evaluator: builtin.task_adherence
//	    initialization_parameters:
//	      model: gpt-5.6-luna
//	      threshold: 3
//	  - evaluator: support-agent-quality
//	    name: quality_strict
//	    version: "2"
//	    data_mapping:
//	      query: "{{item.customer_message}}"
type EvaluatorRef struct {
	// Evaluator is the evaluator to run: a catalog name or builtin.<name>.
	Evaluator string `yaml:"evaluator" json:"evaluator"`
	// Name labels the criterion in results. Empty means the evaluator's name.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// Version pins this reference. Pinning belongs to one eval's reference
	// rather than to the asset, matching evaluator_version on the criterion.
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
	// InitializationParameters carry the judge deployment and a built-in's
	// numeric threshold. They are bound against the evaluator's published
	// contract rather than forwarded as written.
	InitializationParameters map[string]any `yaml:"initialization_parameters,omitempty" json:"initialization_parameters,omitempty"`
	// DataMapping binds evaluator inputs to dataset columns, and is written
	// only when the inference from declared inputs and columns gets it wrong.
	DataMapping map[string]string `yaml:"data_mapping,omitempty" json:"data_mapping,omitempty"`
}

// IsBuiltin reports whether the reference names a platform evaluator, which
// needs no catalog entry and is never uploaded.
func (e EvaluatorRef) IsBuiltin() bool {
	return strings.HasPrefix(e.Evaluator, BuiltinPrefix)
}

// APIName is the name the service expects, with the builtin prefix removed.
func (e EvaluatorRef) APIName() string {
	return strings.TrimPrefix(e.Evaluator, BuiltinPrefix)
}

// CriterionName labels this criterion in results.
func (e EvaluatorRef) CriterionName() string {
	if e.Name != "" {
		return e.Name
	}
	return e.APIName()
}

// EvaluatorList is a sequence of EvaluatorRef.
//
// A bare string is refused rather than accepted quietly. Every other collection
// in the file is a list of named maps, and a bare string would have to mean the
// evaluator while reading as the criterion's own name — a different key this
// same entry also carries.
type EvaluatorList []EvaluatorRef

func (el *EvaluatorList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.SequenceNode {
		return messages.EvaluatorsMustBeSequence(value.Kind)
	}

	result := make([]EvaluatorRef, 0, len(value.Content))
	for _, node := range value.Content {
		switch node.Kind {
		case yaml.ScalarNode:
			var name string
			if err := node.Decode(&name); err != nil {
				return messages.DecodingEvaluatorName(err)
			}
			return messages.BareEvaluatorEntry(name)
		case yaml.MappingNode:
			ref, err := decodeEvaluatorRef(node)
			if err != nil {
				return err
			}
			if ref.Evaluator == "" {
				return messages.EvaluatorEntryMissingEvaluator()
			}
			result = append(result, ref)
		default:
			return messages.EvaluatorEntryMustBeMapping(node.Kind)
		}
	}

	*el = result
	return nil
}

// decodeEvaluatorRef decodes one entry with the strictness the file promises.
//
// yaml.Node.Decode does not inherit KnownFields from the decoder that reached
// it, so `verison:` inside an evaluator entry was dropped in silence while the
// same typo one level up was named. Round-tripping the node through a strict
// decoder restores it; the error keeps yaml's own "field X not found in type Y"
// shape, which the caller rewrites into the file's vocabulary.
func decodeEvaluatorRef(node *yaml.Node) (EvaluatorRef, error) {
	raw, err := yaml.Marshal(node)
	if err != nil {
		return EvaluatorRef{}, messages.DecodingEvaluator(err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)

	var ref EvaluatorRef
	if err := decoder.Decode(&ref); err != nil {
		return EvaluatorRef{}, messages.DecodingEvaluator(rebaseYAMLLines(err, node.Line))
	}
	return ref, nil
}

// yamlErrorLine matches the line number yaml puts on each unmarshal error.
var yamlErrorLine = regexp.MustCompile(`line (\d+):`)

// rebaseYAMLLines moves line numbers from the extracted snippet back onto the
// file, so the reader is pointed at the key they typed rather than at line 2.
func rebaseYAMLLines(err error, startLine int) error {
	if startLine <= 0 {
		return err
	}
	return errors.New(yamlErrorLine.ReplaceAllStringFunc(err.Error(), func(m string) string {
		n, convErr := strconv.Atoi(yamlErrorLine.FindStringSubmatch(m)[1])
		if convErr != nil {
			return m
		}
		return fmt.Sprintf("line %d:", startLine+n-1)
	}))
}

// UnmarshalJSON accepts the same mapping-only form as the YAML decoder.
//
// This matters for the service-target provider: azd hands the service entry to
// the extension as JSON, so a config written the old way arrives here as a bare
// string and has to be refused with the same remedy rather than with a
// decoder's own type error.
func (el *EvaluatorList) UnmarshalJSON(data []byte) error {
	var entries []json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		return messages.EvaluatorsMustBeList(err)
	}

	result := make([]EvaluatorRef, 0, len(entries))
	for _, entry := range entries {
		trimmed := bytes.TrimSpace(entry)
		if len(trimmed) > 0 && trimmed[0] == '"' {
			var name string
			if err := json.Unmarshal(trimmed, &name); err != nil {
				return messages.DecodingEvaluatorName(err)
			}
			return messages.BareEvaluatorEntry(name)
		}

		var ref EvaluatorRef
		if err := json.Unmarshal(trimmed, &ref); err != nil {
			return messages.DecodingEvaluator(err)
		}
		if ref.Evaluator == "" {
			return messages.EvaluatorEntryMissingEvaluator()
		}
		result = append(result, ref)
	}

	*el = result
	return nil
}

// MarshalJSON is the default list encoding, defined so a compact form cannot
// creep back in through the encoder.
//
// Everything the reference carries has to survive the round trip: the eval
// fingerprint is taken over this encoding, so a field dropped here is a change
// the reconciler cannot see.
func (el EvaluatorList) MarshalJSON() ([]byte, error) {
	// Aliased so the element encoder does not recurse through this method.
	type ref = EvaluatorRef

	out := make([]any, 0, len(el))
	for _, r := range el {
		out = append(out, ref(r))
	}
	return json.Marshal(out)
}

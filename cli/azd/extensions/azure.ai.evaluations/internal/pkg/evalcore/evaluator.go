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
	// Ref carries a `$ref` an author wrote here. Core splices the directive into
	// any object, so a reference kept in its own file deploys; modelling it is
	// what stops the editing read from refusing the file that deploy accepts.
	Ref string `yaml:"$ref,omitempty" json:"$ref,omitempty"`
	// Evaluator is the evaluator to run: a catalog name or builtin.<name>.
	// Empty only on an entry that is still a `$ref`, before it is resolved.
	Evaluator string `yaml:"evaluator,omitempty" json:"evaluator,omitempty"`
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
		return messages.EvaluatorsMustBeSequence(nodeKindName(value.Kind))
	}

	result := make([]EvaluatorRef, 0, len(value.Content))
	for _, entry := range value.Content {
		// Resolved before the kind is read, so `- *base` -- an entry that is
		// itself an alias, which is the most natural thing an anchor is for --
		// is read as the mapping it names rather than refused for being an
		// alias. Resolved once: doing it again inside the decode would find
		// nothing left and report that nothing had expanded.
		node, expanded, err := resolveAliases(entry, map[*yaml.Node]bool{})
		if err != nil {
			return err
		}

		switch node.Kind {
		case yaml.ScalarNode:
			var name string
			if err := node.Decode(&name); err != nil {
				return messages.DecodingEvaluatorName(err)
			}
			return messages.BareEvaluatorEntry(name)
		case yaml.MappingNode:
			ref, err := decodeEvaluatorRef(node, entry.Line, expanded)
			if err != nil {
				return err
			}
			// An entry that is still a `$ref` has no name here: the file it
			// points at supplies one, and resolution consumes the directive. So
			// this stays strict on the resolved reading, where an entry with
			// neither is genuinely malformed, without refusing an include the
			// deploy path accepts.
			if ref.Evaluator == "" && ref.Ref == "" {
				return messages.EvaluatorEntryMissingEvaluator()
			}
			result = append(result, ref)
		default:
			return messages.EvaluatorEntryMustBeMapping(nodeKindName(node.Kind))
		}
	}

	*el = result
	return nil
}

// nodeKindName names a yaml.Kind in the file's vocabulary.
//
// yaml.Kind is an unnamed uint32 with no String method, so a message built with
// %v read "evaluator entry must be a mapping, got 16".
func nodeKindName(kind yaml.Kind) string {
	switch kind {
	case yaml.DocumentNode:
		return "a document"
	case yaml.SequenceNode:
		return "a list"
	case yaml.MappingNode:
		return "a mapping"
	case yaml.ScalarNode:
		return "a single value"
	case yaml.AliasNode:
		return "an alias"
	default:
		return "something else"
	}
}

// decodeEvaluatorRef decodes one already-resolved entry with the strictness the
// file promises.
//
// yaml.Node.Decode does not inherit KnownFields from the decoder that reached
// it, so `verison:` inside an evaluator entry was dropped in silence while the
// same typo one level up was named. Round-tripping the node through a strict
// decoder restores it; the error keeps yaml's own "field X not found in type Y"
// shape, which the caller rewrites into the file's vocabulary.
//
// entryLine is where the entry begins in the file, and expanded says whether an
// alias was replaced on the way here -- between them they decide how much of
// the snippet's line numbering can be trusted back onto the file.
func decodeEvaluatorRef(node *yaml.Node, entryLine int, expanded bool) (EvaluatorRef, error) {
	raw, err := yaml.Marshal(node)
	if err != nil {
		return EvaluatorRef{}, messages.DecodingEvaluator(err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)

	var ref EvaluatorRef
	if err := decoder.Decode(&ref); err != nil {
		return EvaluatorRef{}, messages.DecodingEvaluator(rebaseYAMLLines(err, entryLine, expanded))
	}
	return ref, nil
}

// resolveAliases copies node with every alias replaced by what it names, and
// reports whether it replaced any.
//
// The strict decode above re-serializes one entry, which lifts it out of the
// document and away from the anchors its aliases point at: `*judge` defined on
// a sibling entry, and `<<: *base`, decoded against an empty anchor table and
// failed a file that had loaded before. Resolving first keeps both working
// without giving up the strictness.
//
// active holds the anchors being expanded on this path. yaml permits an anchor
// that contains its own alias, which would otherwise expand forever.
func resolveAliases(node *yaml.Node, active map[*yaml.Node]bool) (*yaml.Node, bool, error) {
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		if active[node.Alias] {
			return nil, false, messages.EvaluatorAliasIsCircular(node.Value)
		}
		active[node.Alias] = true
		defer delete(active, node.Alias)
		resolved, _, err := resolveAliases(node.Alias, active)
		return resolved, true, err
	}

	// The anchor is dropped with the alias it fed: keeping it would emit the
	// same name twice once two aliases resolve to it.
	copied := *node
	copied.Anchor = ""
	copied.Content = make([]*yaml.Node, len(node.Content))
	expanded := false
	for i, child := range node.Content {
		resolvedChild, childExpanded, err := resolveAliases(child, active)
		if err != nil {
			return nil, false, err
		}
		copied.Content[i] = resolvedChild
		expanded = expanded || childExpanded
	}
	return &copied, expanded, nil
}

// yamlErrorLine matches the line number yaml puts on each unmarshal error.
var yamlErrorLine = regexp.MustCompile(`line (\d+):`)

// rebaseYAMLLines moves line numbers from the extracted snippet back onto the
// file, so the reader is pointed at the key they typed rather than at line 2.
//
// Only line-for-line while the snippet is what the file holds. An alias expands
// to more lines than the `*name` it replaced, so an offset computed from the
// snippet lands somewhere further down the file -- on a valid, unrelated entry,
// or past the end. Where anything expanded the whole entry is named instead:
// less precise, and never a confident accusation against code that is fine.
func rebaseYAMLLines(err error, entryLine int, expanded bool) error {
	if entryLine <= 0 {
		return err
	}
	return errors.New(yamlErrorLine.ReplaceAllStringFunc(err.Error(), func(m string) string {
		if expanded {
			return fmt.Sprintf("line %d:", entryLine)
		}
		n, convErr := strconv.Atoi(yamlErrorLine.FindStringSubmatch(m)[1])
		if convErr != nil {
			return m
		}
		return fmt.Sprintf("line %d:", entryLine+n-1)
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

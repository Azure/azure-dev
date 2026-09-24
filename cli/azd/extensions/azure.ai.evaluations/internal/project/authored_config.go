// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"

	"azureaieval/internal/messages"

	"github.com/braydonk/yaml"
)

// Catalog sections of an evaluation configuration.
const (
	SectionDatasets   = "datasets"
	SectionEvaluators = "evaluators"
	SectionEvals      = "evals"
)

// AuthoredEntry is how one catalog entry was written.
type AuthoredEntry struct {
	// Name is the entry's own name, empty when the entry is only a `$ref` and
	// takes its name from the file it points at.
	Name string
	// Ref is the `$ref` the entry carries, empty when it is written out here.
	Ref string
	// HasDefinition is an evaluator holding its rubric in place.
	HasDefinition bool
	// Version pins an already-registered version.
	Version string
	// SupportedEvaluationLevels is local evaluator metadata, not a resolved
	// service schema. Absence or an unfamiliar shape leaves compatibility unknown.
	SupportedEvaluationLevels []string
}

// AuthoredConfig answers what a configuration already declares, as written.
//
// The commands that read, modify and save the configuration need the authored
// shape rather than the resolved one: a `$ref` they inline and save orphans the
// file it named. They cannot take it from a decoded configuration either,
// because decoding is strict and runs after resolution -- by then a `$ref` is
// either gone or an unknown key. So they read the document.
type AuthoredConfig struct {
	sections map[string][]AuthoredEntry
}

// ReadAuthoredConfig reads the configuration as written, without resolving
// includes or decoding it. A configuration that is not there yet reads as empty
// rather than as an error: `generate` runs before `init` on the golden path.
func ReadAuthoredConfig(evalDir string) (*AuthoredConfig, error) {
	if err := checkOneConfig(evalDir); err != nil {
		return nil, err
	}
	doc, err := readConfigDocument(resolvedConfigPath(evalDir))
	if err != nil {
		return nil, err
	}
	return authoredFromDocument(doc), nil
}

// ReadAuthoredDataset reads only the named dataset's name and local file for
// authoring preflight. Local includes use the normal resolver, including
// ref-only entries when the name is not written here. Unrelated named entries
// are not resolved, and unknown fields are not strictly decoded or rewritten.
// File is resolved against the configuration directory; nil means no match.
func ReadAuthoredDataset(location, name string) (*DatasetDecl, error) {
	path, err := ResolveEvalConfigPath(location)
	if err != nil {
		return nil, err
	}
	doc, err := readConfigDocument(path)
	if err != nil {
		return nil, err
	}
	root, err := documentMapping(doc)
	if err != nil {
		return nil, err
	}
	entries, err := mappingSequence(root, SectionDatasets)
	if err != nil {
		return nil, err
	}
	var candidates []*yaml.Node
	for _, item := range entries.Content {
		declaredName := scalarUnder(item, "name")
		if declaredName == name {
			candidates = []*yaml.Node{item}
			break
		}
		if declaredName == "" && nodeUnder(item, refDirective) != nil {
			candidates = append(candidates, item)
		}
	}
	for _, item := range candidates {
		var raw map[string]any
		if err := item.Decode(&raw); err != nil {
			return nil, messages.ParsingEvalConfig(path, err)
		}
		resolved, err := resolveEvalRefs(raw, EvalDirOf(path))
		if err != nil {
			return nil, err
		}
		if resolved["name"] != name {
			continue
		}
		file, ok := resolved["file"].(string)
		if value := resolved["file"]; value != nil && !ok {
			return nil, messages.ParsingEvalConfig(path, fmt.Errorf("dataset %q file must be a string", name))
		}
		return &DatasetDecl{Name: name, File: ResolveSource(EvalDirOf(path), file)}, nil
	}
	return nil, nil
}

// authoredFromDocument reads the three catalogs in document order.
//
// A document this cannot read answers with nothing rather than an error: the
// callers use it to decide what is already declared, and the write path refuses
// a wrongly shaped file with a message of its own.
func authoredFromDocument(doc *yaml.Node) *AuthoredConfig {
	out := &AuthoredConfig{sections: map[string][]AuthoredEntry{}}
	root, err := documentMapping(doc)
	if err != nil {
		return out
	}
	for _, section := range []string{SectionDatasets, SectionEvaluators, SectionEvals} {
		seq, err := mappingSequence(root, section)
		if err != nil {
			continue
		}
		for _, item := range seq.Content {
			if item.Kind != yaml.MappingNode {
				continue
			}
			out.sections[section] = append(out.sections[section], AuthoredEntry{
				Name:                      scalarUnder(item, "name"),
				Ref:                       scalarUnder(item, refDirective),
				HasDefinition:             nodeUnder(item, "definition") != nil,
				Version:                   scalarUnder(item, "version"),
				SupportedEvaluationLevels: authoredEvaluationLevels(item),
			})
		}
	}
	return out
}

func authoredEvaluationLevels(item *yaml.Node) []string {
	node := nodeUnder(item, "supported_evaluation_levels")
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil
	}
	var levels []string
	for _, value := range node.Content {
		if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
			return nil
		}
		levels = append(levels, value.Value)
	}
	return levels
}

// Entry reports how the named entry of a section was written.
func (a *AuthoredConfig) Entry(section, name string) (AuthoredEntry, bool) {
	if a == nil {
		return AuthoredEntry{}, false
	}
	for _, entry := range a.sections[section] {
		if entry.Name == name {
			return entry, true
		}
	}
	return AuthoredEntry{}, false
}

// Names lists the entries a section declares, in document order. An entry that
// is only a `$ref` names nothing here and is left out.
func (a *AuthoredConfig) Names(section string) []string {
	if a == nil {
		return nil
	}
	var names []string
	for _, entry := range a.sections[section] {
		if entry.Name != "" {
			names = append(names, entry.Name)
		}
	}
	return names
}

// HasUnnamedRef reports whether a section holds an entry that is only a `$ref`,
// whose name lives in the file it points at and so cannot be matched here.
func (a *AuthoredConfig) HasUnnamedRef(section string) bool {
	if a == nil {
		return false
	}
	for _, entry := range a.sections[section] {
		if entry.Name == "" && entry.Ref != "" {
			return true
		}
	}
	return false
}

// scalarUnder returns the scalar value of key, or "".
func scalarUnder(mapping *yaml.Node, key string) string {
	if node := nodeUnder(mapping, key); node != nil && node.Kind == yaml.ScalarNode {
		return node.Value
	}
	return ""
}

// nodeUnder returns the value node for key, or nil.
func nodeUnder(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

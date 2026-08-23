// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"errors"
	"os"

	"azureaieval/internal/messages"

	"github.com/braydonk/yaml"
)

// ScaffoldWrite is what `init` decided to add to the configuration.
//
// Only the new entries, and at most one removal. What the author already wrote
// is not represented here at all, which is what keeps it from being rewritten.
type ScaffoldWrite struct {
	// RemoveEval is the eval `--force` replaces, dropped before the new one is
	// appended. Empty when nothing is being replaced.
	RemoveEval string
	Datasets   []DatasetDecl
	Evaluators []EvaluatorDecl
	Evals      []Eval
}

// Empty reports whether this would change nothing.
func (w ScaffoldWrite) Empty() bool {
	return w.RemoveEval == "" && len(w.Datasets) == 0 && len(w.Evaluators) == 0 && len(w.Evals) == 0
}

// ApplyScaffold writes what `init` decided, editing the file rather than
// rewriting it.
//
// The same reasoning as UpsertCatalogEntry, for the command that adds an eval:
// decisions are made from the decoded configuration, but only the new entries
// are written, so comments and any key these structs do not model survive.
func ApplyScaffold(evalDir string, write ScaffoldWrite) error {
	if write.Empty() {
		return nil
	}
	if err := checkOneConfig(evalDir); err != nil {
		return err
	}
	if err := os.MkdirAll(evalDir, 0o750); err != nil {
		return messages.Creating(evalDir, err)
	}
	path := resolvedConfigPath(evalDir)

	doc, err := readConfigDocument(path)
	if err != nil {
		return err
	}
	root := documentMapping(doc)

	if write.RemoveEval != "" {
		removeSequenceEntryNamed(mappingSequence(root, "evals"), write.RemoveEval)
	}
	for _, decl := range write.Datasets {
		if err := appendEncoded(mappingSequence(root, "datasets"), decl); err != nil {
			return err
		}
	}
	for _, decl := range write.Evaluators {
		if err := appendEncoded(mappingSequence(root, "evaluators"), decl); err != nil {
			return err
		}
	}
	for _, eval := range write.Evals {
		if err := appendEncoded(mappingSequence(root, "evals"), eval); err != nil {
			return err
		}
	}

	out, err := marshalConfigDocument(doc)
	if err != nil {
		return messages.SerializingEvalConfig(err)
	}
	return writeConfigBytes(path, out)
}

// appendEncoded renders one entry and appends it to a sequence.
//
// Through the struct's own yaml tags, so a field added to the model is written
// without this file having to learn about it.
func appendEncoded(seq *yaml.Node, entry any) error {
	body, err := yaml.Marshal(entry)
	if err != nil {
		return messages.SerializingEvalConfig(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return messages.SerializingEvalConfig(err)
	}
	if len(doc.Content) == 0 {
		return nil
	}
	seq.Content = append(seq.Content, doc.Content[0])
	return nil
}

// removeSequenceEntryNamed drops the entry whose `name` is name.
func removeSequenceEntryNamed(seq *yaml.Node, name string) {
	kept := seq.Content[:0]
	for _, item := range seq.Content {
		if mappingHasName(item, name) {
			continue
		}
		kept = append(kept, item)
	}
	seq.Content = kept
}

// readConfigDocument parses the configuration at path, answering an empty
// document when there is nothing there yet.
func readConfigDocument(path string) (*yaml.Node, error) {
	body, err := os.ReadFile(path)
	switch {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		return &yaml.Node{}, nil
	default:
		return nil, messages.ReadingEvalConfig(path, err)
	}

	doc := &yaml.Node{}
	if len(body) > 0 {
		if err := yaml.Unmarshal(body, doc); err != nil {
			return nil, messages.ParsingEvalConfig(path, err)
		}
	}
	return doc, nil
}

// UpsertCatalogEntry records one field on a named catalog entry, editing the
// file rather than rewriting it.
//
// `generate` used to read the configuration into structs, change a field, and
// marshal the whole thing back. That deleted every comment the author had
// written and changed the indentation of the file, and it silently dropped any
// key the structs did not model -- which is why `$ref` had to be added to three
// of them, and why a directive anywhere the structs did not reach could not
// survive an edit at all.
//
// Editing the node tree fixes both at once: what this function does not touch
// is written back exactly as it was found, whether or not this package knows
// what it means.
//
// kind is the top-level sequence (`datasets` or `evaluators`), field the key to
// set on the matched entry. Reports whether anything changed, and whether the
// file had to be created.
func UpsertCatalogEntry(evalDir, kind, name, field, value string) (changed bool, created bool, err error) {
	if err := checkOneConfig(evalDir); err != nil {
		return false, false, err
	}
	if err := os.MkdirAll(evalDir, 0o750); err != nil {
		return false, false, messages.Creating(evalDir, err)
	}
	path := resolvedConfigPath(evalDir)

	if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
		created = true
	}
	doc, err := readConfigDocument(path)
	if err != nil {
		return false, false, err
	}

	root := documentMapping(doc)
	seq := mappingSequence(root, kind)
	entry := sequenceEntryNamed(seq, name)

	if entry == nil {
		seq.Content = append(seq.Content, catalogEntryNode(name, field, value))
	} else if !setMappingScalar(entry, field, value) {
		// The entry already says this. Rewriting the file to change nothing
		// would still rewrite it, and this is the repeated-generate path.
		return false, false, nil
	}

	out, err := marshalConfigDocument(doc)
	if err != nil {
		return false, false, messages.SerializingEvalConfig(err)
	}
	if err := writeConfigBytes(path, out); err != nil {
		return false, false, err
	}
	return true, created, nil
}

// documentMapping returns the mapping at the root of doc, filling in an empty
// document so a configuration that does not exist yet can be built up.
func documentMapping(doc *yaml.Node) *yaml.Node {
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		// A configuration that is not a mapping is not one this can edit; the
		// decoder reports it properly, so leave it to the read path.
		root.Kind = yaml.MappingNode
		root.Tag = "!!map"
		root.Content = nil
		root.Value = ""
	}
	return root
}

// mappingSequence returns the sequence under key, adding it when absent.
func mappingSequence(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != key {
			continue
		}
		value := mapping.Content[i+1]
		if value.Kind != yaml.SequenceNode {
			value.Kind = yaml.SequenceNode
			value.Tag = "!!seq"
			value.Value = ""
		}
		return value
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		seq)
	return seq
}

// sequenceEntryNamed returns the mapping in seq whose `name` is name.
//
// An entry that is only a `$ref` has no name here, and is deliberately not
// matched: what it declares is decided by the file it points at, and the
// caller's guard refuses those before this is reached.
func sequenceEntryNamed(seq *yaml.Node, name string) *yaml.Node {
	for _, item := range seq.Content {
		if mappingHasName(item, name) {
			return item
		}
	}
	return nil
}

// mappingHasName reports whether a sequence entry declares this name here.
func mappingHasName(item *yaml.Node, name string) bool {
	if item.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(item.Content); i += 2 {
		if item.Content[i].Value == "name" && item.Content[i+1].Value == name {
			return true
		}
	}
	return false
}

// setMappingScalar sets key on mapping, reporting whether that changed anything.
func setMappingScalar(mapping *yaml.Node, key, value string) bool {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != key {
			continue
		}
		if mapping.Content[i+1].Value == value {
			return false
		}
		mapping.Content[i+1].Value = value
		mapping.Content[i+1].Tag = "!!str"
		mapping.Content[i+1].Style = 0
		return true
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
	return true
}

// catalogEntryNode builds the entry appended for a name the file does not
// declare yet.
func catalogEntryNode(name, field, value string) *yaml.Node {
	return &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "name"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: field},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
		},
	}
}

// marshalConfigDocument renders the edited tree at the indentation azd uses for
// azure.yaml, so an edited file keeps the shape of the ones beside it.
func marshalConfigDocument(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

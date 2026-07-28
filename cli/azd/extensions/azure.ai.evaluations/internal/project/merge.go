// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"
)

// ArtifactRef is a name/source pair written back into the deployment spec after
// generation.
type ArtifactRef struct {
	Name   string
	Source string
}

// MergeArtifactRefs writes `source:` references for generated artifacts into
// the deployment spec, matching entries by name and appending when absent.
//
// It edits the document through the yaml Node API rather than round-tripping
// through structs, so comments, key order, and formatting survive. Only the
// `source` key of a matched entry is touched; anything the developer hand-edited
// is left alone.
func MergeArtifactRefs(path string, datasets, evaluators []ArtifactRef) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %q: %w", path, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing %q: %w", path, err)
	}

	root := documentRoot(&doc)
	if root == nil {
		return fmt.Errorf("%q is not a YAML mapping", path)
	}

	if err := mergeSection(root, "datasets", datasets); err != nil {
		return fmt.Errorf("%q: %w", path, err)
	}
	if err := mergeSection(root, "evaluators", evaluators); err != nil {
		return fmt.Errorf("%q: %w", path, err)
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("serializing %q: %w", path, err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("writing %q: %w", path, err)
	}
	return nil
}

// documentRoot unwraps the document node to the top-level mapping.
func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil
	}
	return doc
}

// mergeSection updates or appends entries in a top-level sequence.
func mergeSection(root *yaml.Node, key string, refs []ArtifactRef) error {
	if len(refs) == 0 {
		return nil
	}

	seq := findOrCreateSequence(root, key)
	if seq == nil {
		return fmt.Errorf("%q is present but is not a sequence", key)
	}

	for _, ref := range refs {
		if entry := findEntryByName(seq, ref.Name); entry != nil {
			setMappingValue(entry, "source", ref.Source)
			continue
		}
		seq.Content = append(seq.Content, newArtifactNode(ref))
	}
	return nil
}

// findOrCreateSequence returns the sequence node for key, creating an empty one
// when the key is absent.
func findOrCreateSequence(root *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != key {
			continue
		}
		value := root.Content[i+1]
		if value.Kind == yaml.SequenceNode {
			return value
		}
		// An explicit null is treated as an empty sequence.
		if value.Tag == "!!null" {
			value.Kind = yaml.SequenceNode
			value.Tag = "!!seq"
			value.Value = ""
			return value
		}
		return nil
	}

	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	seqNode := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	root.Content = append(root.Content, keyNode, seqNode)
	return seqNode
}

// findEntryByName locates a mapping entry whose `name` matches.
func findEntryByName(seq *yaml.Node, name string) *yaml.Node {
	for _, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		if mappingValue(item, "name") == name {
			return item
		}
	}
	return nil
}

// mappingValue reads a scalar value from a mapping node.
func mappingValue(node *yaml.Node, key string) string {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1].Value
		}
	}
	return ""
}

// setMappingValue updates a scalar in place, or appends it when absent. Only
// the targeted key is touched.
func setMappingValue(node *yaml.Node, key, value string) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content[i+1].Kind = yaml.ScalarNode
			node.Content[i+1].Tag = "!!str"
			node.Content[i+1].Value = value
			return
		}
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}

// newArtifactNode builds a fresh `{name, source}` entry.
func newArtifactNode(ref ArtifactRef) *yaml.Node {
	return &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "name"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: ref.Name},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "source"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: ref.Source},
		},
	}
}

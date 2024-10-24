// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// yamlnode allows for manipulation of YAML nodes using a dotted-path syntax.
//
// Examples of dotted-path syntax:
// - a.object.key
// - b.item_list[1]
package yamlnode

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/braydonk/yaml"
)

var ErrNodeNotFound = errors.New("node not found")

// ErrNodeWrongKind is returned when the node kind is not as expected.
//
// This error may be useful for nodes that have multiple possible kinds.
var ErrNodeWrongKind = errors.New("unexpected node kind")

// Find retrieves a node at the given path.
func Find(root *yaml.Node, path string) (*yaml.Node, error) {
	parts, err := parsePath(path)
	if err != nil {
		return nil, err
	}

	found := find(root, parts)
	if found == nil {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, path)
	}

	return found, nil
}

// Set sets the node at the given path to the provided value.
func Set(root *yaml.Node, path string, value *yaml.Node) error {
	parts, err := parsePath(path)
	if err != nil {
		return err
	}

	// find the anchor node
	anchor := find(root, parts[:len(parts)-1])
	if anchor == nil {
		return fmt.Errorf("%w: %s", ErrNodeNotFound, path)
	}

	// set the node
	seek, isKey := parts[len(parts)-1].(string)
	idx, isSequence := parts[len(parts)-1].(int)

	if isKey {
		if anchor.Kind != yaml.MappingNode {
			return fmt.Errorf("%w: %s is not a mapping node", ErrNodeWrongKind, parts[len(parts)-1])
		}

		for i := 0; i < len(anchor.Content); i += 2 {
			if anchor.Content[i].Value == seek {
				anchor.Content[i+1] = value
				return nil
			}
		}

		anchor.Content = append(anchor.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: seek})
		anchor.Content = append(anchor.Content, value)
	} else if isSequence {
		if anchor.Kind != yaml.SequenceNode {
			return fmt.Errorf("%w: %s is not a sequence node", ErrNodeWrongKind, parts[len(parts)-1])
		}

		if idx < 0 || idx > len(anchor.Content) {
			return fmt.Errorf("array index out of bounds: %d", idx)
		}

		anchor.Content[idx] = value
	}

	return nil
}

// Append appends a node to the sequence (array) node at the given path.
//
// If the node at the path is not a sequence node, ErrNodeWrongKind is returned.
func Append(root *yaml.Node, path string, node *yaml.Node) error {
	parts, err := parsePath(path)
	if err != nil {
		return err
	}

	// find the anchor node
	found := find(root, parts)
	if found == nil {
		return fmt.Errorf("%w: %s", ErrNodeNotFound, path)
	}

	if found.Kind != yaml.SequenceNode {
		return fmt.Errorf("%w %d for append", ErrNodeWrongKind, found.Kind)
	}

	found.Content = append(found.Content, node)
	return nil
}

// Encode encodes a value into a YAML node.
func Encode(value interface{}) (*yaml.Node, error) {
	var node yaml.Node
	err := node.Encode(value)
	if err != nil {
		return nil, fmt.Errorf("encoding yaml node: %w", err)
	}

	return &node, nil
}

func find(current *yaml.Node, parts []any) *yaml.Node {
	if len(parts) == 0 {
		// we automatically skip the document node to avoid having to specify it in the path
		if current.Kind == yaml.DocumentNode {
			return current.Content[0]
		}

		return current
	}

	seek, _ := parts[0].(string)
	idx, isArray := parts[0].(int)

	switch current.Kind {
	case yaml.DocumentNode:
		// we automatically skip the document node to avoid having to specify it in the path
		return find(current.Content[0], parts)
	case yaml.MappingNode:
		for i := 0; i < len(current.Content); i += 2 {
			if current.Content[i].Value == seek {
				return find(current.Content[i+1], parts[1:])
			}
		}
	case yaml.SequenceNode:
		if isArray && idx < len(current.Content) {
			return find(current.Content[idx], parts[1:])
		}
	}

	return nil
}

// parsePath parses a dotted path into a slice of parts, where each part is either a string or an integer.
// The integer parts represent array indexes, and the string parts represent keys.
func parsePath(path string) ([]any, error) {
	if path == "" {
		return nil, errors.New("empty path")
	}

	// future: support escaping dots
	parts := strings.Split(path, ".")
	expanded, err := expandArrays(parts)
	if err != nil {
		return nil, err
	}

	return expanded, nil
}

// expandArrays expands array indexing into individual elements.
func expandArrays(parts []string) (expanded []any, err error) {
	expanded = make([]interface{}, 0, len(parts))
	for _, s := range parts {
		before, after := cutBrackets(s)
		expanded = append(expanded, before)

		if len(after) > 0 {
			content := after[1 : len(after)-1]
			idx, err := strconv.Atoi(content)
			if err != nil {
				return nil, fmt.Errorf("invalid array index: %s in %s", content, after)
			}

			expanded = append(expanded, idx)
		}
	}

	return expanded, nil
}

// cutBrackets splits a string into two parts, before the brackets, and after the brackets.
func cutBrackets(s string) (before string, after string) {
	if len(s) > 0 && s[len(s)-1] == ']' { // reverse check for faster exit
		for i := len(s) - 1; i >= 0; i-- {
			if s[i] == '[' {
				return s[:i], s[i:]
			}
		}
	}

	return s, ""
}

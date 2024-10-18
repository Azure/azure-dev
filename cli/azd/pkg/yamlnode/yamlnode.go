// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package yamlnode allows for manipulation of YAML nodes using a dotted-path syntax.
//
// Examples of dotted-path syntax:
//   - a.map.key
//   - b.items[1]
//
// When using [Set] or [Append], an optional qualifier `?` can be used in a path element
// to indicate that the node is conditionally present, and should be created if not present.
// A preceding bracket-pair, `[]`, can be used to indicate that a sequence node should be created.
//
// Optional qualifier examples:
//   - a?.map.key - if 'a' is not present, it will be created
//   - a.map?.key - if 'map' is not present, it will be created
//   - b.items[]? - if 'items' is not present, it will be created as a sequence
//
// The special characters in a dotted-path syntax are:
//   - `.` (dot) - separates key elements
//   - `[` (open bracket) - used in sequences
//   - `]` (close bracket) - used in sequences
//   - `?` (question mark) - optional qualifier
//   - `"` (double quote) - used to indicate a quoted-string
//
// If these special characters are part of a key, the key can be surrounded as a quoted-string using `"` (double quotes)
// to indicate their literalness.
// If a `"` (double-quote) character is also part of the key, a preceding backslash `\"` may be used to escape it.
//
// Quoted-string examples:
//   - "esc.ape.d" -> esc.ape.d
//   - "\"hello\"..[world]" -> "hello"..[world]
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
// This error may be useful to detect nodes that have multiple possible kinds and need to be handled specially.
var ErrNodeWrongKind = errors.New("unexpected node kind")

// Find retrieves a node at the given path.
//
// Examples of dotted-paths:
//   - a.map.key
//   - b.items[1]
func Find(root *yaml.Node, path string) (*yaml.Node, error) {
	parts, err := parsePath(path)
	if err != nil {
		return nil, err
	}

	found, err := find(root, parts, true)
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, path)
	}

	return found, nil
}

// Set sets the node at the path to the provided value.
//
// An optional qualifier `?` can be used automatically create node(s) that are conditionally present.
//
// Examples:
//   - a?.map.key - if 'a' is not present, it will be created
//   - a.map?.key - if 'map' is not present, it will be created
func Set(root *yaml.Node, path string, value *yaml.Node) error {
	parts, err := parsePath(path)
	if err != nil {
		return err
	}
	// find the anchor node
	anchor, err := find(root, parts[:len(parts)-1], false)
	if err != nil {
		return err
	}
	if anchor == nil {
		return fmt.Errorf("%w: %s", ErrNodeNotFound, path)
	}

	part := parts[len(parts)-1]
	switch part.kind {
	case keyElem:
		if anchor.Kind != yaml.MappingNode {
			return fmt.Errorf("%w: %s is not a mapping node", ErrNodeWrongKind, parts[len(parts)-1].key)
		}

		for i := 0; i < len(anchor.Content); i += 2 {
			if anchor.Content[i].Value == part.key {
				anchor.Content[i+1] = value
				return nil
			}
		}

		anchor.Content = append(anchor.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: part.key})
		anchor.Content = append(anchor.Content, value)
	case indexElem:
		if anchor.Kind != yaml.SequenceNode {
			return fmt.Errorf("%w: %s is not a sequence node", ErrNodeWrongKind, parts[len(parts)-2].key)
		}

		if part.idx < 0 || part.idx > len(anchor.Content) {
			return fmt.Errorf("sequence index out of bounds: %d", part.idx)
		}

		anchor.Content[part.idx] = value
	}

	return nil
}

// Append appends a node to the sequence node at the given path.
// If the node at the path is not a sequence node, ErrNodeWrongKind is returned.
//
// An optional qualifier `?` can be used automatically create node(s) that are conditionally present;
// a preceding bracket-pair, `[]`, is used to indicate sequences.
//
// Examples:
//   - a?.map.items - if 'a' is not present, it will be created
//   - b.items[]? - if 'items' is not present, it will be created as a sequence
func Append(root *yaml.Node, path string, node *yaml.Node) error {
	parts, err := parsePath(path)
	if err != nil {
		return err
	}

	// find the anchor node
	found, err := find(root, parts, false)
	if err != nil {
		return err
	}

	if found == nil {
		return fmt.Errorf("%w: %s", ErrNodeNotFound, path)
	}

	if found.Kind != yaml.SequenceNode {
		return fmt.Errorf("append to a non-sequence node: %w", ErrNodeWrongKind)
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

// find retrieves a node at the given path.
func find(current *yaml.Node, parts []pathElem, findOnly bool) (*yaml.Node, error) {
	if len(parts) == 0 {
		// we automatically skip the document node to avoid having to specify it in the path
		if current.Kind == yaml.DocumentNode {
			return current.Content[0], nil
		}

		return current, nil
	}

	part := parts[0]

	switch current.Kind {
	case yaml.DocumentNode:
		// we automatically skip the document node to avoid having to specify it in the path
		return find(current.Content[0], parts, findOnly)
	case yaml.MappingNode:
		if part.kind != keyElem {
			return nil, fmt.Errorf("%w: unexpected %s as a mapping node", ErrNodeWrongKind, part.key)
		}

		for i := 0; i < len(current.Content); i += 2 {
			if current.Content[i].Value == part.key {
				return find(current.Content[i+1], parts[1:], findOnly)
			}
		}
	case yaml.SequenceNode:
		if part.kind != indexElem {
			return nil, fmt.Errorf("%w: unexpected %s as a sequence node", ErrNodeWrongKind, part.key)
		}

		if part.idx < len(current.Content) {
			return find(current.Content[part.idx], parts[1:], findOnly)
		}
	}

	if findOnly { // if we are only looking for the node, we won't honor optional
		return nil, nil
	}

	if part.optionalKind == 0 {
		return nil, nil
	}

	node := &yaml.Node{Kind: part.optionalKind}

	switch current.Kind {
	case yaml.MappingNode:
		current.Content = append(current.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: part.key})
		current.Content = append(current.Content, node)
	case yaml.SequenceNode:
		current.Content[part.idx] = node
	}

	return node, nil
}

// parsePath parses a dotted-path into a slice of yaml path elements.
func parsePath(s string) ([]pathElem, error) {
	elem := strings.Builder{}
	parsed := []pathElem{}

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '.':
			if elem.Len() == 0 {
				if i == 0 {
					return nil, fmt.Errorf("unexpected dot '.' at the beginning of the path")
				}

				return nil, fmt.Errorf("unexpected dot '.' at the beginning of the path near %s", s[i-1:])
			}

			yamlPaths, err := parseElem(elem.String())
			if err != nil {
				return nil, fmt.Errorf("parsing %s: %w", elem.String(), err)
			}

			parsed = append(parsed, yamlPaths...)
			elem.Reset()
		case '"':
			j := i + 1
			elem.WriteByte('"')

			// find the unescaped, closing quote
			// note that we just want to preserve the quoted string as-is to avoid treating '.' as a separator,
			// since a second pass will parse the quoted string
			for j < len(s) {
				if s[j] == '"' && s[j-1] != '\\' {
					elem.WriteByte('"')
					break
				}
				elem.WriteByte(s[j])
				j++
			}
			i = j
		default:
			elem.WriteByte(c)
		}
	}

	if elem.Len() > 0 {
		yamlPaths, err := parseElem(elem.String())
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", elem.String(), err)
		}
		parsed = append(parsed, yamlPaths...)
	}

	if len(parsed) == 0 {
		return nil, fmt.Errorf("empty path")
	}

	return parsed, nil
}

// parseElem parses a dotted-path element into the corresponding yaml path element(s).
func parseElem(s string) ([]pathElem, error) {
	result := []pathElem{}
	elem := pathElem{kind: keyElem}
	key := strings.Builder{}

	inKey := true // whether we are currently parsing a key part of the element
	for i := 0; i < len(s); i++ {
		c := s[i]

		switch c {
		case '[':
			inKey = false

			// find the closing bracket
			j := i + 1
			for j < len(s) {
				if s[j] == ']' {
					break
				}
				j++
			}

			if j == len(s) {
				return nil, fmt.Errorf("missing closing bracket ']' after '[': %s", s[i:])
			}

			// contents is the string between the brackets
			contents := s[i+1 : j]
			if contents == "" && j+1 < len(s) && s[j+1] == '?' { // empty brackets followed by '?'
				elem.optionalKind = yaml.SequenceNode
				i = j + 1
				continue
			}
			idx, err := strconv.Atoi(contents)
			if err != nil || idx < 0 {
				return nil, fmt.Errorf("invalid sequence index: %s in %s", contents, s[i:j+1])
			}

			switch elem.kind {
			case keyElem:
				elem.key = key.String()
				key.Reset()

				if elem.key == "" {
					return nil, fmt.Errorf("empty key in %s", s)
				}
			case indexElem:
				// do nothing
			}

			result = append(result, elem)
			elem = pathElem{kind: indexElem, idx: idx}

			i = j
		case ']':
			return nil, fmt.Errorf("unexpected closing bracket '[' before ']': %s", s[i:])
		case '?':
			elem.optionalKind = yaml.MappingNode
			if i != len(s)-1 {
				return nil, fmt.Errorf(
					"unexpected characters after optional qualifier `?`: %s: "+
						"'?' is a special character; to escape using double quotes, try \"%s\"",
					s[i+1:],
					s[:i])
			}
		case '\\':
			if i+1 < len(s) && s[i+1] == '"' {
				key.WriteByte('"')
				i++
			}
		case '"':
			// find the closing quote
			j := i + 1
			for j < len(s) {
				if s[j] == '\\' && j+1 < len(s) && s[j+1] == '"' {
					key.WriteByte('"')
					j += 2
					continue
				}

				if s[j] == '"' {
					break
				}
				key.WriteByte(s[j])
				j++
			}

			if j == len(s) {
				return nil, fmt.Errorf(
					"missing closing quote '\"' near %s; to escape double quotes, try adding a preceding backslash", s[i:])
			}
			i = j
		default:
			if !inKey {
				return nil, fmt.Errorf(
					"unexpected characters after character `%s`: %s: "+
						"'[', ']' are special characters; to escape using double quotes, try \"%s\"",
					string(s[i-1]),
					s[i:],
					s[:i-1])
			}
			key.WriteByte(c)
		}
	}

	if key.Len() > 0 {
		elem.key = key.String()
		elem.kind = keyElem
	}

	if len(result) == 0 && elem.key == "" {
		return nil, fmt.Errorf("empty")
	}

	result = append(result, elem)
	return result, nil
}

type kind int

const (
	keyElem kind = 1 << iota
	indexElem
)

// pathElem represents a single element in a YAML syntax tree.
//
// Each element is either a key (for a mapping node) or an index (for a sequence node).
type pathElem struct {
	// kind is the kind of the current path element.
	// either key or idx will be set, but not both.
	kind kind

	// key is the name of the current node.
	key string
	// idx is index of the current sequence node.
	idx int

	// optionalKind is the kind of node that will be created if not present.
	// It is only used in set or append operations.
	optionalKind yaml.Kind
}

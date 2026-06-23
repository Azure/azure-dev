// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/yamlnode"
	"github.com/braydonk/yaml"
)

// ErrServiceNotFound is returned when a named service entry is absent and creation was not
// requested. It mirrors yamlnode.ErrNodeNotFound so callers can branch on a sentinel.
var ErrServiceNotFound = errors.New("service entry not found")

// EditTarget selects where a write to a service entry lands.
type EditTarget int

const (
	// EditInline writes the field on the service entry in the holding document, beside any
	// $ref. This is the spec §2.4 default (append inline): ResolveFileRefs reads such a key as
	// an overlay override layered on top of the referenced file, so the write is read back as
	// the winning value.
	EditInline EditTarget = iota

	// EditRefFile follows the entry's $ref and writes the field into the referenced split file
	// instead. If the entry is not $ref-backed it falls back to an inline write.
	EditRefFile
)

// YAMLDocument is an editable, comment-preserving azure.yaml (or a referenced split file). It
// wraps a braydonk yaml.Node tree so edits keep comments, key order, and block-scalar style,
// matching how azd core round-trips azure.yaml.
//
// It is the $ref-aware write counterpart to ResolveFileRefs: EntryRef recognizes a $ref entry
// with the same key the resolver uses, and EditRefFile resolves the split-file path with the
// resolver's shared path logic, so reads and writes of $ref entries agree. It is also intended
// for the #8049 composition command write path.
//
// Edits mutate the tree in memory; call Save to persist. Writes through EditRefFile lazily load
// the referenced file, and Save persists every file touched this way alongside the main one.
type YAMLDocument struct {
	path    string
	root    yaml.Node
	refDocs map[string]*YAMLDocument
}

// LoadYAMLDocument reads and parses the YAML file at path into an editable document.
func LoadYAMLDocument(path string) (*YAMLDocument, error) {
	// #nosec G304 -- azure.yaml and its $ref split files are trusted config input, the same
	// trust level as azure.yaml itself (design spec §2.4 treats includes as trusted input).
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fileRefValidation(
			fmt.Sprintf("cannot read YAML file %q: %v", path, err),
			"Check that the path is correct and the file exists and is readable.",
		)
	}
	return ParseYAMLDocument(path, data)
}

// ParseYAMLDocument parses data as the YAML document located (logically) at path. path is used
// to resolve relative $ref split-file targets and as the destination for Save; it need not
// exist on disk yet. Empty input yields an empty document whose root is created on first edit.
func ParseYAMLDocument(path string, data []byte) (*YAMLDocument, error) {
	doc := &YAMLDocument{path: path, refDocs: map[string]*YAMLDocument{}}
	if len(bytes.TrimSpace(data)) == 0 {
		return doc, nil
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.SetScanBlockScalarAsLiteral(true)
	if err := decoder.Decode(&doc.root); err != nil {
		return nil, fileRefValidation(
			fmt.Sprintf("YAML file %q is not valid: %v", path, err),
			"Fix the file so it parses as a YAML document.",
		)
	}
	return doc, nil
}

// Bytes serializes the document, preserving comments, key order, and block-scalar style with
// two-space indentation (the azure.yaml convention used by azd core).
func (d *YAMLDocument) Bytes() ([]byte, error) {
	if d.root.Kind == 0 {
		return []byte{}, nil
	}

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	// preserve multi-line block scalar style
	encoder.SetAssumeBlockAsLiteral(true)
	if err := encoder.Encode(&d.root); err != nil {
		_ = encoder.Close()
		return nil, fmt.Errorf("encoding YAML: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("closing YAML encoder: %w", err)
	}
	return buf.Bytes(), nil
}

// Save writes the document back to its path, then persists every referenced split file edited
// through EditRefFile.
func (d *YAMLDocument) Save() error {
	data, err := d.Bytes()
	if err != nil {
		return err
	}
	if err := os.WriteFile(d.path, data, osutil.PermissionFile); err != nil {
		return fmt.Errorf("writing %q: %w", d.path, err)
	}
	for _, sub := range d.refDocs {
		if err := sub.Save(); err != nil {
			return err
		}
	}
	return nil
}

// ServiceEntry returns the mapping node for services.<name>. When create is true a missing
// services mapping and/or entry are added (and returned); when false a missing entry returns
// ErrServiceNotFound.
func (d *YAMLDocument) ServiceEntry(name string, create bool) (*yaml.Node, error) {
	if name == "" {
		return nil, errors.New("service name must not be empty")
	}

	services, err := d.servicesNode(create)
	if err != nil {
		return nil, err
	}
	if services != nil {
		if entry := mappingValue(services, name); entry != nil {
			if entry.Kind != yaml.MappingNode {
				return nil, fmt.Errorf("%w: service %q is not a mapping", yamlnode.ErrNodeWrongKind, name)
			}
			return entry, nil
		}
	}

	if !create {
		return nil, fmt.Errorf("%w: %s", ErrServiceNotFound, name)
	}

	entry := &yaml.Node{Kind: yaml.MappingNode}
	services.Content = append(services.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: name},
		entry,
	)
	return entry, nil
}

// SetServiceField sets services.<name>.<key> to value, creating the service entry if missing.
// It is $ref-aware:
//
//   - EditInline writes the key on the entry node beside any $ref. ResolveFileRefs reads it as
//     an overlay override on top of the referenced file.
//   - EditRefFile writes the key into the file named by the entry's $ref instead (resolved
//     relative to this document with the resolver's shared path logic). If the entry has no
//     $ref it falls back to an inline write.
//
// value may be any value yamlnode.Encode accepts (scalars, sequences, mappings).
func (d *YAMLDocument) SetServiceField(name, key string, value any, target EditTarget) error {
	if key == "" {
		return errors.New("field key must not be empty")
	}

	entry, err := d.ServiceEntry(name, true)
	if err != nil {
		return err
	}

	valueNode, err := yamlnode.Encode(value)
	if err != nil {
		return err
	}

	if target == EditRefFile {
		if ref, isRef := EntryRef(entry); isRef {
			return d.setRefFileField(ref, key, valueNode)
		}
		// No split file to target; fall back to an inline write.
	}

	return setEntryField(entry, key, valueNode)
}

// EntryRef reports the $ref target of a service entry and whether the entry is $ref-backed. It
// recognizes the same directive key as ResolveFileRefs, so a writer and the resolver agree on
// which entries are file includes.
func EntryRef(entry *yaml.Node) (string, bool) {
	value := mappingValue(entry, refKey)
	if value == nil || value.Kind != yaml.ScalarNode {
		return "", false
	}
	ref := strings.TrimSpace(value.Value)
	if ref == "" {
		return "", false
	}
	return ref, true
}

// setRefFileField loads (and caches) the split file named by ref and sets key on its root
// mapping. The file is persisted by Save.
func (d *YAMLDocument) setRefFileField(ref, key string, valueNode *yaml.Node) error {
	target, err := refTargetPath(ref, d.dir())
	if err != nil {
		return err
	}

	sub, err := d.refDoc(target)
	if err != nil {
		return err
	}

	subRoot, err := sub.rootMapping(true)
	if err != nil {
		return err
	}
	return setEntryField(subRoot, key, valueNode)
}

// refDoc returns the cached editable document for the split file at target, loading it once.
func (d *YAMLDocument) refDoc(target string) (*YAMLDocument, error) {
	if sub, ok := d.refDocs[target]; ok {
		return sub, nil
	}
	sub, err := LoadYAMLDocument(target)
	if err != nil {
		return nil, err
	}
	d.refDocs[target] = sub
	return sub, nil
}

// dir returns the directory that holds the document, used to resolve relative $ref targets.
func (d *YAMLDocument) dir() string {
	if d.path == "" {
		return "."
	}
	return filepath.Dir(d.path)
}

// servicesNode returns the top-level services mapping, optionally creating it.
func (d *YAMLDocument) servicesNode(create bool) (*yaml.Node, error) {
	root, err := d.rootMapping(create)
	if err != nil || root == nil {
		return nil, err
	}

	if services := mappingValue(root, "services"); services != nil {
		if services.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%w: services is not a mapping", yamlnode.ErrNodeWrongKind)
		}
		return services, nil
	}

	if !create {
		return nil, nil
	}

	services := &yaml.Node{Kind: yaml.MappingNode}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "services"},
		services,
	)
	return services, nil
}

// rootMapping returns the document's root mapping node, optionally initializing an empty
// document and root mapping.
func (d *YAMLDocument) rootMapping(create bool) (*yaml.Node, error) {
	if d.root.Kind == 0 {
		if !create {
			return nil, nil
		}
		d.root = yaml.Node{Kind: yaml.DocumentNode}
	}
	if d.root.Kind != yaml.DocumentNode {
		return nil, fmt.Errorf("%w: YAML root is not a document", yamlnode.ErrNodeWrongKind)
	}

	if len(d.root.Content) == 0 {
		if !create {
			return nil, nil
		}
		d.root.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
	}

	root := d.root.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%w: YAML root is not a mapping", yamlnode.ErrNodeWrongKind)
	}
	return root, nil
}

// mappingValue returns the value node for key in a mapping node, or nil when absent.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// setEntryField sets key on the mapping node via yamlnode.Set, first transferring the comments
// of any replaced value onto the new node so an inline annotation survives an in-place update.
// A new key is appended (preserving existing key order).
func setEntryField(entry *yaml.Node, key string, valueNode *yaml.Node) error {
	if old := mappingValue(entry, key); old != nil {
		valueNode.HeadComment = old.HeadComment
		valueNode.LineComment = old.LineComment
		valueNode.FootComment = old.FootComment
	}
	return yamlnode.Set(entry, quotePathSegment(key), valueNode)
}

// quotePathSegment escapes a single yamlnode dotted-path segment so a key containing the path
// syntax's special characters (. [ ] ? ") is treated literally.
func quotePathSegment(segment string) string {
	if !strings.ContainsAny(segment, `.[]?"`) {
		return segment
	}
	return `"` + strings.ReplaceAll(segment, `"`, `\"`) + `"`
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package yamlnode

import (
	"reflect"
	"testing"

	"github.com/braydonk/yaml"
)

const doc = `
root:
  nested:
    key: value
  array:
    - item1
    - item2
    - item3
  empty: []
  mixedArray:
    - stringItem
    - nestedObj:
        deepKey: deepValue
    - nestedArr:
        - item1
        - item2
`

func TestFind(t *testing.T) {
	var root yaml.Node
	err := yaml.Unmarshal([]byte(doc), &root)
	if err != nil {
		t.Fatalf("Failed to unmarshal YAML: %v", err)
	}

	tests := []struct {
		name     string
		path     string
		expected interface{}
		wantErr  bool
	}{
		{"Simple path", "root.nested.key", "value", false},
		{"Array index", "root.array[1]", "item2", false},
		{"Nested array object", "root.mixedArray[1].nestedObj.deepKey", "deepValue", false},

		{"Map", "root.nested", map[string]string{"key": "value"}, false},
		{"Array", "root.array", []string{"item1", "item2", "item3"}, false},

		{"Non-existent path", "root.nonexistent", "", true},
		{"Invalid array index", "root.array[3]", "", true},
		{"Invalid path format", "root.array.[1]", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := Find(&root, tt.path)

			if (err != nil) != tt.wantErr {
				t.Errorf("Get() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				assertNodeEquals(t, "Get()", node, tt.expected)
			}
		})
	}
}

func TestSet(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		value   interface{}
		wantErr bool
	}{
		{"root", "root", "new_value", false},

		{"Update", "root.nested.key", "new_value", false},
		{"Update object", "root.nested", map[string]string{"new_key": "new_value"}, false},
		{"Update array", "root.array[1]", "new_item2", false},
		{"Update nested array object", "root.mixedArray[1].nestedObj.deepKey", "new_deep_value", false},

		{"Create", "root.nested.new_key", "brand_new", false},
		{"Create array", "root.new_array", []string{"first_item"}, false},
		{"Create object", "root.nested.new_object", map[string]string{"key": "value"}, false},
		{"Create nested array object", "root.mixedArray[1].nestedObj.newKey", "new_deep_value", false},
		{"Create missing key", "root.nonexistent?.key", "value", false},

		{"Invalid path", "root.nonexistent.key", "value", true},
		{"Invalid array index", "root.array[10]", "value", true},
		{"Invalid path format", "root.array.[1]", "value", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var root yaml.Node
			err := yaml.Unmarshal([]byte(doc), &root)
			if err != nil {
				t.Fatalf("Failed to unmarshal YAML: %v", err)
			}

			valueNode, err := Encode(tt.value)
			if err != nil {
				t.Fatalf("Failed to encode value: %v", err)
			}

			err = Set(&root, tt.path, valueNode)

			if (err != nil) != tt.wantErr {
				t.Errorf("Set() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Verify the set value
				node, err := Find(&root, tt.path)
				if err != nil {
					t.Errorf("Failed to get set value: %v", err)
					return
				}

				assertNodeEquals(t, "Set()", node, tt.value)
			}
		})
	}
}

func TestAppend(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		value    interface{} // Value to append
		wantErr  bool
		checkLen int // Expected length after append
	}{
		{"Append to array", "root.array", "item4", false, 4},
		{"Append to empty array", "root.empty", "item1", false, 1},
		{"Append object to mixed array", "root.mixedArray", map[string]string{"key": "value"}, false, 4},
		{"Append to nested array", "root.mixedArray[2].nestedArr", "item3", false, 3},
		{"Append to non-existent array", "root.nonexistent[]?", "item1", false, 1},
		{"Invalid path (not an array)", "root.nested.key", "invalid", true, 0},
		{"Non-existent path", "root.nonexistent", "value", true, 0},
		{"Invalid path format", "root.array.[1]", "invalid", true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var root yaml.Node
			err := yaml.Unmarshal([]byte(doc), &root)
			if err != nil {
				t.Fatalf("Failed to unmarshal YAML: %v", err)
			}

			valueNode, err := Encode(tt.value)
			if err != nil {
				t.Fatalf("Failed to encode value: %v", err)
			}

			err = Append(&root, tt.path, valueNode)

			if (err != nil) != tt.wantErr {
				t.Errorf("Append() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Verify the append operation
				node, err := Find(&root, tt.path)
				if err != nil {
					t.Errorf("Failed to get appended value: %v", err)
					return
				}
				if node.Kind != yaml.SequenceNode {
					t.Errorf("Append() did not result in a sequence node at path %s", tt.path)
					return
				}
				if len(node.Content) != tt.checkLen {
					t.Errorf("Append() resulted in wrong length = %d, want %d", len(node.Content), tt.checkLen)
					return
				}
				// we verify the last node matches the appended value
				lastNode := node.Content[len(node.Content)-1]
				assertNodeEquals(t, "Append()", lastNode, tt.value)
			}
		})
	}
}

func assertNodeEquals(t *testing.T, funcName string, node *yaml.Node, expected interface{}) {
	t.Helper()
	wantStr, err := yaml.Marshal(expected)
	if err != nil {
		t.Fatalf("Failed to marshal expected: %v", err)
	}

	gotStr, err := yaml.Marshal(node)
	if err != nil {
		t.Fatalf("Failed to marshal node: %v", err)
	}

	if string(gotStr) != string(wantStr) {
		t.Errorf("%s = %v, want %v", funcName, string(gotStr), string(wantStr))
	}
}

func TestParseElem(t *testing.T) {
	tests := []struct {
		name, input string
		expected    []pathElem
		wantErr     bool
	}{
		{name: "key", input: "simple_key", expected: []pathElem{{kind: keyElem, key: "simple_key"}}},
		{name: "key with optional", input: "simple_key?",
			expected: []pathElem{
				{kind: keyElem, key: "simple_key", optionalKind: yaml.MappingNode},
			}},
		{
			name: "index", input: "key[1]",
			expected: []pathElem{
				{kind: keyElem, key: "key"},
				{kind: indexElem, idx: 1},
			},
		},
		{
			name: "optional sequence", input: "key[]?",
			expected: []pathElem{
				{kind: keyElem, key: "key", optionalKind: yaml.SequenceNode},
			},
		},
		{
			name: "multiple indices", input: "nested[1][2]",
			expected: []pathElem{
				{kind: keyElem, key: "nested"},
				{kind: indexElem, idx: 1},
				{kind: indexElem, idx: 2},
			},
		},
		{
			name: "quoted key", input: "\"special.[]\\\"-characters?\"",
			expected: []pathElem{
				{kind: keyElem, key: "special.[]\"-characters?"},
			},
		},
		{
			name: "quoted index", input: "\"special.[]\\\"-characters?\"[1]",
			expected: []pathElem{
				{kind: keyElem, key: "special.[]\"-characters?"},
				{kind: indexElem, idx: 1},
			},
		},
		{
			name: "quote escaping", input: "a \\\"nice\\\" key",
			expected: []pathElem{
				{kind: keyElem, key: "a \"nice\" key"},
			},
		},
		// Error cases
		{name: "invalid index format", input: "hello[a]", wantErr: true},
		{name: "missing closing bracket", input: "hello[1", wantErr: true},
		{name: "missing opening bracket", input: "hello]22", wantErr: true},
		{name: "empty key", input: "[1]", wantErr: true},
		{name: "empty input", input: "", wantErr: true},
		{name: "invalid characters in key", input: "hello]world[1]", wantErr: true},
		{name: "invalid characters in key", input: "hello?world[1]", wantErr: true},
		{name: "negative index", input: "array[-1]", wantErr: true},
		{name: "missing closed quotes", input: "ar\"ray[-1]", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseElem(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("parseElem(%q) error = nil, wantErr = true", tt.input)
				}
				return
			}

			if err != nil {
				t.Errorf("parseElem(%q) unexpected error = %v", tt.input, err)
				return
			}

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("parseElem(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParsePath(t *testing.T) {
	tests := []struct {
		name, input string
		expected    []pathElem
		wantErr     bool
	}{
		{name: "key", input: "simple_key", expected: []pathElem{{kind: keyElem, key: "simple_key"}}},
		{name: "nested key", input: "parent.nested", expected: []pathElem{
			{kind: keyElem, key: "parent"},
			{kind: keyElem, key: "nested"},
		}},
		{name: "nested array", input: "parent.nested[1].under", expected: []pathElem{
			{kind: keyElem, key: "parent"},
			{kind: keyElem, key: "nested"},
			{kind: indexElem, idx: 1},
			{kind: keyElem, key: "under"},
		}},
		{name: "more nested", input: "parent.nested[1].under?.array[]?.next", expected: []pathElem{
			{kind: keyElem, key: "parent"},
			{kind: keyElem, key: "nested"},
			{kind: indexElem, idx: 1},
			{kind: keyElem, key: "under", optionalKind: yaml.MappingNode},
			{kind: keyElem, key: "array", optionalKind: yaml.SequenceNode},
			{kind: keyElem, key: "next"},
		}},
		{name: "escaping", input: "\"start.\\\".end\"", expected: []pathElem{
			{kind: keyElem, key: "start.\".end"},
		}},
		{name: "escaping middle", input: "start.\"start.\\\".[]?end[]\".end", expected: []pathElem{
			{kind: keyElem, key: "start"},
			{kind: keyElem, key: "start.\".[]?end[]"},
			{kind: keyElem, key: "end"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePath(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parsePath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("parsePath() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOutputFormat_Constants(t *testing.T) {
	tests := []struct {
		name     string
		format   OutputFormat
		expected string
	}{
		{
			name:     "FormatTable",
			format:   FormatTable,
			expected: "table",
		},
		{
			name:     "FormatJSON",
			format:   FormatJSON,
			expected: "json",
		},
		{
			name:     "FormatYAML",
			format:   FormatYAML,
			expected: "yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, OutputFormat(tt.expected), tt.format)
		})
	}
}

func TestOutputFormat_String(t *testing.T) {
	tests := []struct {
		name     string
		format   OutputFormat
		expected string
	}{
		{
			name:     "Table",
			format:   FormatTable,
			expected: "table",
		},
		{
			name:     "JSON",
			format:   FormatJSON,
			expected: "json",
		},
		{
			name:     "YAML",
			format:   FormatYAML,
			expected: "yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, string(tt.format))
		})
	}
}

func TestPrintObject_UnsupportedFormat(t *testing.T) {
	obj := struct{ Name string }{Name: "test"}

	err := PrintObject(obj, "unsupported")

	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported format")
}

func TestPrintObject_TableWithNonStructSlice(t *testing.T) {
	// Table format requires struct or slice
	err := PrintObject("string value", FormatTable)

	require.Error(t, err)
	require.Contains(t, err.Error(), "requires a struct or slice")
}

type TestStructWithTableTag struct {
	Name  string `table:"Name"`
	Value int    `table:"Value"`
}

type TestStructWithoutTableTag struct {
	Name  string
	Value int
}

func TestGetTableColumns(t *testing.T) {
	t.Run("WithTableTags", func(t *testing.T) {
		s := TestStructWithTableTag{Name: "test", Value: 42}

		// Test that struct with table tags can be printed
		err := PrintObject(s, FormatTable)

		// Should succeed (prints to stdout, but we can't easily capture it)
		require.NoError(t, err)
	})

	t.Run("Slice", func(t *testing.T) {
		slice := []TestStructWithTableTag{
			{Name: "item1", Value: 1},
			{Name: "item2", Value: 2},
		}

		err := PrintObject(slice, FormatTable)
		require.NoError(t, err)
	})
}

func TestPrintObject_JSONFormat(t *testing.T) {
	tests := []struct {
		name    string
		obj     any
		wantErr bool
	}{
		{
			name: "SimpleStruct",
			obj:  struct{ Name string }{Name: "test"},
		},
		{
			name: "Map",
			obj:  map[string]int{"count": 5},
		},
		{
			name: "Nil",
			obj:  nil,
		},
		{
			name: "EmptyStruct",
			obj:  struct{}{},
		},
		{
			name: "EmptySlice",
			obj:  []string{},
		},
		{
			name: "NestedStruct",
			obj: struct {
				Name    string
				Details struct {
					Age int
				}
			}{Name: "test", Details: struct{ Age int }{Age: 25}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := PrintObject(tt.obj, FormatJSON)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPrintObject_YAMLFormat(t *testing.T) {
	tests := []struct {
		name    string
		obj     any
		wantErr bool
	}{
		{
			name: "SimpleStruct",
			obj:  struct{ Name string }{Name: "test"},
		},
		{
			name: "Map",
			obj:  map[string]int{"count": 5},
		},
		{
			name: "Nil",
			obj:  nil,
		},
		{
			name: "NestedStruct",
			obj: struct {
				Name    string
				Details struct {
					Age int
				}
			}{Name: "test", Details: struct{ Age int }{Age: 25}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := PrintObject(tt.obj, FormatYAML)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPrintObject_TableFormat(t *testing.T) {
	type StructWithTableTags struct {
		Name  string `table:"Name"`
		Value int    `table:"Value"`
	}

	tests := []struct {
		name    string
		obj     any
		wantErr bool
		errMsg  string
	}{
		{
			name: "StructWithTableTags",
			obj:  StructWithTableTags{Name: "test", Value: 42},
		},
		{
			name: "SliceWithTableTags",
			obj: []StructWithTableTags{
				{Name: "item1", Value: 1},
				{Name: "item2", Value: 2},
			},
		},
		{
			name:    "StructWithoutTableTags",
			obj:     struct{ Name string }{Name: "test"},
			wantErr: true,
			errMsg:  "no fields with table tags found",
		},
		{
			name:    "String",
			obj:     "not a struct",
			wantErr: true,
			errMsg:  "requires a struct or slice",
		},
		{
			name:    "Int",
			obj:     42,
			wantErr: true,
			errMsg:  "requires a struct or slice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := PrintObject(tt.obj, FormatTable)

			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPrintObject_PointerDereference(t *testing.T) {
	type StructWithTableTags struct {
		Name string `table:"Name"`
	}

	s := &StructWithTableTags{Name: "test"}

	// PrintObject should dereference the pointer for table format
	err := PrintObject(s, FormatTable)

	require.NoError(t, err)
}

func TestDateTimeFormat(t *testing.T) {
	// Verify the date time format constant is defined
	require.Equal(t, "2006-01-02 15:04", dateTimeFormat)
}

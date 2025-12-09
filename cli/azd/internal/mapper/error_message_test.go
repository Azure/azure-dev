// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mapper

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test how error messages look with different types, especially pointers
func TestErrorMessagesWithDifferentTypes(t *testing.T) {
	clearRegistry()

	type CustomStruct struct {
		Name string
	}

	tests := []struct {
		name          string
		srcType       interface{}
		dstType       interface{}
		expectedParts []string
	}{
		{
			name:          "simple types",
			srcType:       "",
			dstType:       0,
			expectedParts: []string{"string", "int"},
		},
		{
			name:          "pointer types",
			srcType:       (*string)(nil),
			dstType:       (*int)(nil),
			expectedParts: []string{"*string", "int"}, // dst is the element type, not the pointer
		},
		{
			name:          "struct types",
			srcType:       CustomStruct{},
			dstType:       "",
			expectedParts: []string{"mapper.CustomStruct", "string"},
		},
		{
			name:          "pointer to struct",
			srcType:       (*CustomStruct)(nil),
			dstType:       (*string)(nil),
			expectedParts: []string{"*mapper.CustomStruct", "string"}, // dst is the element type
		},
		{
			name:          "slice types",
			srcType:       []string{},
			dstType:       []int{},
			expectedParts: []string{"[]string", "[]int"},
		},
		{
			name:          "map types",
			srcType:       map[string]int{},
			dstType:       map[int]string{},
			expectedParts: []string{"map[string]int", "map[int]string"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Test NoMapperError
			var dstVal interface{}
			switch tc.dstType.(type) {
			case string, *string:
				var s string
				dstVal = &s
			case int, *int:
				var i int
				dstVal = &i
			case []int:
				var s []int
				dstVal = &s
			case map[int]string:
				var m map[int]string
				dstVal = &m
			default:
				var s string
				dstVal = &s
			}

			err := Convert(tc.srcType, dstVal)
			require.Error(t, err)

			// Check that the error message contains expected type names
			for _, part := range tc.expectedParts {
				assert.Contains(t, err.Error(), part, "Error message should contain type: %s", part)
			}
		})
	}
}

// Test conversion error messages
func TestConversionErrorMessages(t *testing.T) {
	clearRegistry()

	// Register a mapper that always fails
	MustRegister(func(ctx context.Context, src string) (*int, error) {
		return nil, errors.New("conversion failed: invalid string format")
	})

	var result *int
	err := Convert("test", &result)
	require.Error(t, err)

	assert.Contains(t, err.Error(), "conversion failed from string to *int")
	assert.Contains(t, err.Error(), "conversion failed: invalid string format")
}

// Test the cleanTypeName function directly
func TestCleanTypeName(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{"nil", nil, "<nil>"},
		{"string", "", "string"},
		{"int", 0, "int"},
		{"pointer to string", (*string)(nil), "*string"},
		{"slice of int", []int{}, "[]int"},
		{"map string to int", map[string]int{}, "map[string]int"},
		{"chan int", make(chan int), "chan int"},
		{"func", func() {}, "func"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var typ reflect.Type
			if tc.input != nil {
				typ = reflect.TypeOf(tc.input)
			}
			result := cleanTypeName(typ)
			assert.Equal(t, tc.expected, result)
		})
	}
}

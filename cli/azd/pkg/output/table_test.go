// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type tableInput struct {
	Size   string
	IsCool bool
}

var tableInputOptions = TableFormatterOptions{
	Columns: []Column{
		{
			Heading:       "Size",
			ValueTemplate: "{{.Size}}",
		},
		{
			Heading:       "Coolness",
			ValueTemplate: "{{.IsCool}}",
		},
		{
			Heading:       "Static",
			ValueTemplate: "Some-Value",
		},
		{
			Heading:       "Lowered",
			ValueTemplate: "Some-Value",
			Transformer:   strings.ToLower,
		},
	},
}

func TestTableFormatterNoColumns(t *testing.T) {
	obj := struct{}{}

	formatter := &TableFormatter{}

	buffer := &bytes.Buffer{}
	err := formatter.Format(obj, buffer, TableFormatterOptions{})
	require.Error(t, err)
}

func TestTableFormatterScalar(t *testing.T) {
	obj := tableInput{
		Size:   "mega",
		IsCool: true,
	}

	formatter := &TableFormatter{}

	buffer := &bytes.Buffer{}
	err := formatter.Format(obj, buffer, tableInputOptions)
	require.NoError(t, err)

	expected := `Size      Coolness  Static      Lowered
mega      true      Some-Value  some-value
`
	require.Equal(t, expected, buffer.String())
}

func TestTableFormatterSlice(t *testing.T) {
	obj := []interface{}{
		tableInput{
			Size:   "mega",
			IsCool: true,
		},
		tableInput{
			Size:   "medium",
			IsCool: false,
		},
	}

	formatter := &TableFormatter{}

	buffer := &bytes.Buffer{}
	err := formatter.Format(obj, buffer, tableInputOptions)
	require.NoError(t, err)

	expected := `Size      Coolness  Static      Lowered
mega      true      Some-Value  some-value
medium    false     Some-Value  some-value
`
	require.Equal(t, expected, buffer.String())
}

func TestTableFormatterNonexistentField(t *testing.T) {
	obj := tableInput{
		Size:   "mega",
		IsCool: true,
	}

	var tableInputOptions = TableFormatterOptions{
		Columns: []Column{
			{
				Heading:       "Size",
				ValueTemplate: "{{.Size}}",
			},
			{
				Heading:       "Unknown",
				ValueTemplate: "{{.FieldDoesNotExist}}",
			},
		},
	}

	formatter := &TableFormatter{}

	buffer := &bytes.Buffer{}
	err := formatter.Format(obj, buffer, tableInputOptions)
	require.Error(t, err)
	require.Contains(t, err.Error(), "can't evaluate field FieldDoesNotExist")
}

func TestTableFormatterSliceConverter(t *testing.T) {
	aStruct := tableInput{
		Size: "medium",
	}
	inputs := []convertInput{
		{
			Name:    "string",
			Input:   "test",
			Success: false,
		},
		{
			Name:    "nil",
			Input:   nil,
			Success: false,
		},
		{
			Name:    "nil pointer",
			Input:   (*tableInput)(nil),
			Success: false,
		},
		{
			Name:    "struct",
			Input:   aStruct,
			Success: true,
			Expected: []interface{}{
				aStruct,
			},
		},
		{
			Name:    "struct pointer",
			Input:   &aStruct,
			Success: true,
			Expected: []interface{}{
				aStruct,
			},
		},
		{
			Name: "slice",
			Input: []interface{}{
				aStruct, &aStruct, "test", []interface{}{},
			},
			Success: true,
			Expected: []interface{}{
				aStruct, &aStruct, "test", []interface{}{},
			},
		},
	}

	for _, input := range inputs {
		t.Run(input.Name, func(t *testing.T) {
			actual, err := convertToSlice(input.Input)
			if input.Success {
				require.NoError(t, err)
				require.Equal(t, input.Expected, actual)
			} else {
				require.Error(t, err)
			}
		})
	}
}

type convertInput struct {
	Name     string
	Input    interface{}
	Success  bool
	Expected []interface{}
}

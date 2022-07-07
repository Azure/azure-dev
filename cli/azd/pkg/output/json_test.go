// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

type jsonInput struct {
	Size   string
	IsCool bool
}

func TestJsonFormatterScalar(t *testing.T) {
	obj := jsonInput{
		Size:   "mega",
		IsCool: true,
	}

	formatter := &JsonFormatter{}

	buffer := &bytes.Buffer{}
	err := formatter.Format(obj, buffer, nil)
	require.NoError(t, err)

	expected := `{
  "Size": "mega",
  "IsCool": true
}
`
	require.Equal(t, expected, buffer.String())
}

func TestJsonFormatterSlice(t *testing.T) {
	obj := []interface{}{
		jsonInput{
			Size:   "mega",
			IsCool: true,
		},
		jsonInput{
			Size:   "medium",
			IsCool: false,
		},
	}

	formatter := &JsonFormatter{}

	buffer := &bytes.Buffer{}
	err := formatter.Format(obj, buffer, nil)
	require.NoError(t, err)

	expected := `[
  {
    "Size": "mega",
    "IsCool": true
  },
  {
    "Size": "medium",
    "IsCool": false
  }
]
`
	require.Equal(t, expected, buffer.String())
}

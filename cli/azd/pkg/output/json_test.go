// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
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
	obj := []any{
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

func TestEventForMessage_StripsAnsiAndAddsNewline(t *testing.T) {
	t.Parallel()
	// ANSI escape: "\x1b[31mred\x1b[0m"
	env := EventForMessage("\x1b[31mred\x1b[0m")
	require.Equal(t, contracts.ConsoleMessageEventDataType, env.Type)
	msg, ok := env.Data.(contracts.ConsoleMessage)
	require.True(t, ok)
	require.Equal(t, "red\n", msg.Message)
	require.False(t, env.Timestamp.IsZero())
}

func TestEventForMessage_EmptyString(t *testing.T) {
	t.Parallel()
	env := EventForMessage("")
	msg, ok := env.Data.(contracts.ConsoleMessage)
	require.True(t, ok)
	require.Equal(t, "\n", msg.Message)
}

func TestJsonFormatter_QueryFilter_NoQueryReturnsInput(t *testing.T) {
	t.Parallel()
	f := &JsonFormatter{}
	obj := map[string]any{"a": 1}
	out, err := f.QueryFilter(obj)
	require.NoError(t, err)
	require.Equal(t, obj, out)
}

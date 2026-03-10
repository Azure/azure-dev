// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestMCPTextResult(t *testing.T) {
	result := MCPTextResult("hello %s, count=%d", "world", 42)

	require.NotNil(t, result)
	require.False(t, result.IsError)
	require.Len(t, result.Content, 1)

	textContent, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "hello world, count=42", textContent.Text)
}

func TestMCPJSONResult_Struct(t *testing.T) {
	type sample struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}
	data := sample{Name: "test", Value: 123}
	result := MCPJSONResult(data)

	require.NotNil(t, result)
	require.False(t, result.IsError)
	require.Len(t, result.Content, 1)

	textContent, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)

	var decoded sample
	err := json.Unmarshal([]byte(textContent.Text), &decoded)
	require.NoError(t, err)
	require.Equal(t, data, decoded)
}

func TestMCPJSONResult_Map(t *testing.T) {
	data := map[string]interface{}{
		"key": "value",
	}
	result := MCPJSONResult(data)

	require.NotNil(t, result)
	require.False(t, result.IsError)
	require.Len(t, result.Content, 1)

	textContent, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)

	var decoded map[string]interface{}
	err := json.Unmarshal([]byte(textContent.Text), &decoded)
	require.NoError(t, err)
	require.Equal(t, "value", decoded["key"])
}

func TestMCPJSONResult_Slice(t *testing.T) {
	data := []string{"a", "b", "c"}
	result := MCPJSONResult(data)

	require.NotNil(t, result)
	require.False(t, result.IsError)
	require.Len(t, result.Content, 1)

	textContent, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)

	var decoded []string
	err := json.Unmarshal([]byte(textContent.Text), &decoded)
	require.NoError(t, err)
	require.Equal(t, data, decoded)
}

func TestMCPJSONResult_Unmarshalable(t *testing.T) {
	ch := make(chan int)
	result := MCPJSONResult(ch)

	require.NotNil(t, result)
	require.True(t, result.IsError)
	require.Len(t, result.Content, 1)

	textContent, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.Contains(t, textContent.Text, "failed to marshal JSON")
}

func TestMCPErrorResult(t *testing.T) {
	result := MCPErrorResult("something failed: %s", "timeout")

	require.NotNil(t, result)
	require.True(t, result.IsError)
	require.Len(t, result.Content, 1)

	textContent, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "something failed: timeout", textContent.Text)
}

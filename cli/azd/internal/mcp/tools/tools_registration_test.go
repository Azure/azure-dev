// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAzdErrorTroubleShootingTool(t *testing.T) {
	t.Parallel()
	tool := NewAzdErrorTroubleShootingTool()

	assert.Equal(t, "error_troubleshooting", tool.Tool.Name)
	assert.NotEmpty(t, tool.Tool.Description)
	assert.NotNil(t, tool.Handler)

	// Verify annotations
	require.NotNil(t, tool.Tool.Annotations.ReadOnlyHint)
	assert.True(t, *tool.Tool.Annotations.ReadOnlyHint)
	require.NotNil(t, tool.Tool.Annotations.IdempotentHint)
	assert.True(t, *tool.Tool.Annotations.IdempotentHint)
	require.NotNil(t, tool.Tool.Annotations.DestructiveHint)
	assert.False(t, *tool.Tool.Annotations.DestructiveHint)
	require.NotNil(t, tool.Tool.Annotations.OpenWorldHint)
	assert.False(t, *tool.Tool.Annotations.OpenWorldHint)
}

func TestNewAzdProvisionCommonErrorTool(t *testing.T) {
	t.Parallel()
	tool := NewAzdProvisionCommonErrorTool()

	assert.Equal(t, "provision_common_error", tool.Tool.Name)
	assert.NotEmpty(t, tool.Tool.Description)
	assert.NotNil(t, tool.Handler)

	// Verify annotations
	require.NotNil(t, tool.Tool.Annotations.ReadOnlyHint)
	assert.True(t, *tool.Tool.Annotations.ReadOnlyHint)
	require.NotNil(t, tool.Tool.Annotations.IdempotentHint)
	assert.True(t, *tool.Tool.Annotations.IdempotentHint)
	require.NotNil(t, tool.Tool.Annotations.DestructiveHint)
	assert.False(t, *tool.Tool.Annotations.DestructiveHint)
	require.NotNil(t, tool.Tool.Annotations.OpenWorldHint)
	assert.False(t, *tool.Tool.Annotations.OpenWorldHint)
}

func TestNewAzdYamlSchemaTool(t *testing.T) {
	t.Parallel()
	tool := NewAzdYamlSchemaTool()

	assert.Equal(t, "validate_azure_yaml", tool.Tool.Name)
	assert.NotEmpty(t, tool.Tool.Description)
	assert.NotNil(t, tool.Handler)

	// Verify annotations
	require.NotNil(t, tool.Tool.Annotations.ReadOnlyHint)
	assert.True(t, *tool.Tool.Annotations.ReadOnlyHint)
	require.NotNil(t, tool.Tool.Annotations.IdempotentHint)
	assert.True(t, *tool.Tool.Annotations.IdempotentHint)
	require.NotNil(t, tool.Tool.Annotations.DestructiveHint)
	assert.False(t, *tool.Tool.Annotations.DestructiveHint)
	require.NotNil(t, tool.Tool.Annotations.OpenWorldHint)
	assert.False(t, *tool.Tool.Annotations.OpenWorldHint)

	// Verify the tool has a "path" input property
	inputSchema := tool.Tool.InputSchema
	pathProp, hasProp := inputSchema.Properties["path"]
	require.True(t, hasProp, "expected 'path' input property")
	propMap, ok := pathProp.(map[string]any)
	require.True(t, ok, "expected property to be a map")
	assert.Equal(t, "string", propMap["type"])
	assert.Contains(t, inputSchema.Required, "path")
}

func TestHandleAzdErrorTroubleShooting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	req := mcp.CallToolRequest{}

	result, err := handleAzdErrorTroubleShooting(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// The handler returns the embedded prompt text
	require.Len(t, result.Content, 1)
	textContent, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "expected TextContent")
	assert.Equal(
		t, prompts.AzdErrorTroubleShootingPrompt, textContent.Text,
	)
}

func TestHandleAzdProvisionCommonError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	req := mcp.CallToolRequest{}

	result, err := handleAzdProvisionCommonError(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// The handler returns the embedded prompt text
	require.Len(t, result.Content, 1)
	textContent, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "expected TextContent")
	assert.Equal(
		t, prompts.AzdProvisionCommonErrorPrompt, textContent.Text,
	)
}

func TestErrorResult(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		msg  string
	}{
		{
			name: "simple message",
			msg:  "something went wrong",
		},
		{
			name: "empty message",
			msg:  "",
		},
		{
			name: "message with special chars",
			msg:  `file "azure.yaml" not found at /tmp/path`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := errorResult(tt.msg)
			require.NotNil(t, result)
			require.Len(t, result.Content, 1)

			textContent, ok := result.Content[0].(mcp.TextContent)
			require.True(t, ok, "expected TextContent")

			// Parse the JSON response
			var resp ErrorResponse
			err := json.Unmarshal(
				[]byte(textContent.Text), &resp,
			)
			require.NoError(t, err)

			assert.True(t, resp.Error)
			assert.Equal(t, tt.msg, resp.Message)
		})
	}
}

func TestErrorResponse_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		resp ErrorResponse
	}{
		{
			name: "error response",
			resp: ErrorResponse{
				Error:   true,
				Message: "validation failed",
			},
		},
		{
			name: "non-error response",
			resp: ErrorResponse{
				Error:   false,
				Message: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.resp)
			require.NoError(t, err)

			var decoded ErrorResponse
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)

			assert.Equal(t, tt.resp, decoded)
		})
	}
}

func TestEmbeddedPrompts_NonEmpty(t *testing.T) {
	t.Parallel()
	assert.NotEmpty(
		t, prompts.AzdErrorTroubleShootingPrompt,
		"embedded prompt should not be empty",
	)
	assert.NotEmpty(
		t, prompts.AzdProvisionCommonErrorPrompt,
		"embedded prompt should not be empty",
	)
}

func TestNewHttpsUrlLoader(t *testing.T) {
	t.Parallel()
	loader := newHttpsUrlLoader()
	require.NotNil(t, loader)
}

func TestToolAnnotations_AllToolsSharePattern(t *testing.T) {
	t.Parallel()
	// All three tools share the same annotation pattern:
	// read-only, idempotent, non-destructive, closed-world
	tools := []struct {
		name string
		tool func() server.ServerTool
	}{
		{"error_troubleshooting", NewAzdErrorTroubleShootingTool},
		{"provision_common_error", NewAzdProvisionCommonErrorTool},
		{"validate_azure_yaml", NewAzdYamlSchemaTool},
	}

	for _, tt := range tools {
		t.Run(tt.name, func(t *testing.T) {
			tool := tt.tool()
			ann := tool.Tool.Annotations

			require.NotNil(t, ann.ReadOnlyHint)
			assert.True(t, *ann.ReadOnlyHint)

			require.NotNil(t, ann.IdempotentHint)
			assert.True(t, *ann.IdempotentHint)

			require.NotNil(t, ann.DestructiveHint)
			assert.False(t, *ann.DestructiveHint)

			require.NotNil(t, ann.OpenWorldHint)
			assert.False(t, *ann.OpenWorldHint)
		})
	}
}

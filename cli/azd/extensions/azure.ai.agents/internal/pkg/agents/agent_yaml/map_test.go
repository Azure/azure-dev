// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"math"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
)

// ---------------------------------------------------------------------------
// constructBuildConfig
// ---------------------------------------------------------------------------

func TestConstructBuildConfig_NoOptions(t *testing.T) {
	t.Parallel()
	cfg := constructBuildConfig()
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.ImageURL != "" {
		t.Errorf("expected empty ImageURL, got %q", cfg.ImageURL)
	}
	if cfg.CPU != "" {
		t.Errorf("expected empty CPU, got %q", cfg.CPU)
	}
	if cfg.Memory != "" {
		t.Errorf("expected empty Memory, got %q", cfg.Memory)
	}
	if cfg.EnvironmentVariables != nil {
		t.Errorf("expected nil EnvironmentVariables, got %v", cfg.EnvironmentVariables)
	}
}

func TestConstructBuildConfig_AllOptions(t *testing.T) {
	t.Parallel()
	cfg := constructBuildConfig(
		WithImageURL("myregistry.azurecr.io/myimage:latest"),
		WithCPU("2"),
		WithMemory("4Gi"),
		WithEnvironmentVariable("KEY1", "val1"),
		WithEnvironmentVariables(map[string]string{"KEY2": "val2", "KEY3": "val3"}),
	)
	if cfg.ImageURL != "myregistry.azurecr.io/myimage:latest" {
		t.Errorf("ImageURL = %q", cfg.ImageURL)
	}
	if cfg.CPU != "2" {
		t.Errorf("CPU = %q", cfg.CPU)
	}
	if cfg.Memory != "4Gi" {
		t.Errorf("Memory = %q", cfg.Memory)
	}
	if len(cfg.EnvironmentVariables) != 3 {
		t.Fatalf("expected 3 env vars, got %d", len(cfg.EnvironmentVariables))
	}
	for _, k := range []string{"KEY1", "KEY2", "KEY3"} {
		if _, ok := cfg.EnvironmentVariables[k]; !ok {
			t.Errorf("missing env var %q", k)
		}
	}
}

// ---------------------------------------------------------------------------
// WithEnvironmentVariable / WithEnvironmentVariables
// ---------------------------------------------------------------------------

func TestWithEnvironmentVariable_InitializesMap(t *testing.T) {
	t.Parallel()
	cfg := &AgentBuildConfig{}
	WithEnvironmentVariable("A", "1")(cfg)
	if cfg.EnvironmentVariables["A"] != "1" {
		t.Errorf("expected A=1, got %q", cfg.EnvironmentVariables["A"])
	}
}

func TestWithEnvironmentVariables_MergesIntoExisting(t *testing.T) {
	t.Parallel()
	cfg := &AgentBuildConfig{EnvironmentVariables: map[string]string{"EXISTING": "x"}}
	WithEnvironmentVariables(map[string]string{"NEW": "y"})(cfg)
	if cfg.EnvironmentVariables["EXISTING"] != "x" {
		t.Error("existing env var was lost")
	}
	if cfg.EnvironmentVariables["NEW"] != "y" {
		t.Error("new env var not set")
	}
}

func TestWithEnvironmentVariables_InitializesNilMap(t *testing.T) {
	t.Parallel()
	cfg := &AgentBuildConfig{}
	WithEnvironmentVariables(map[string]string{"K": "V"})(cfg)
	if cfg.EnvironmentVariables["K"] != "V" {
		t.Errorf("expected K=V")
	}
}

// ---------------------------------------------------------------------------
// convertIntToInt32
// ---------------------------------------------------------------------------

func TestConvertIntToInt32(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   *int
		want    *int32
		wantErr bool
	}{
		{
			name:  "nil input",
			input: nil,
			want:  nil,
		},
		{
			name:  "zero",
			input: new(0),
			want:  new(int32(0)),
		},
		{
			name:  "positive value",
			input: new(42),
			want:  new(int32(42)),
		},
		{
			name:  "negative value",
			input: new(-10),
			want:  new(int32(-10)),
		},
		{
			name:  "max int32",
			input: new(math.MaxInt32),
			want:  new(int32(math.MaxInt32)),
		},
		{
			name:  "min int32",
			input: new(math.MinInt32),
			want:  new(int32(math.MinInt32)),
		},
		{
			name:    "overflow positive",
			input:   new(math.MaxInt32 + 1),
			wantErr: true,
		},
		{
			name:    "overflow negative",
			input:   new(math.MinInt32 - 1),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := convertIntToInt32(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.want == nil {
				if got != nil {
					t.Fatalf("expected nil, got %v", *got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil result")
			}
			if *got != *tc.want {
				t.Errorf("got %d, want %d", *got, *tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// convertFloat64ToFloat32
// ---------------------------------------------------------------------------

func TestConvertFloat64ToFloat32(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input *float64
		isNil bool
	}{
		{name: "nil input", input: nil, isNil: true},
		{name: "zero", input: new(0.0)},
		{name: "typical temperature", input: new(0.7)},
		{name: "one", input: new(1.0)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := convertFloat64ToFloat32(tc.input)
			if tc.isNil {
				if got != nil {
					t.Fatalf("expected nil, got %v", *got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil result")
			}
			expected := float32(*tc.input)
			if *got != expected {
				t.Errorf("got %v, want %v", *got, expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// convertYamlToolToApiTool
// ---------------------------------------------------------------------------

func TestConvertYamlToolToApiTool_Nil(t *testing.T) {
	t.Parallel()
	_, err := convertYamlToolToApiTool(nil)
	if err == nil {
		t.Fatal("expected error for nil tool")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("error should mention nil, got: %s", err.Error())
	}
}

func TestConvertYamlToolToApiTool_UnknownType(t *testing.T) {
	t.Parallel()
	_, err := convertYamlToolToApiTool("not-a-tool")
	if err == nil {
		t.Fatal("expected error for unknown tool type")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("error should mention unsupported, got: %s", err.Error())
	}
}

func TestConvertYamlToolToApiTool_Function(t *testing.T) {
	t.Parallel()
	desc := "adds two numbers"
	yamlTool := FunctionTool{
		Tool: Tool{
			Name:        "add",
			Kind:        ToolKindFunction,
			Description: &desc,
		},
		Parameters: PropertySchema{},
		Strict:     new(true),
	}

	result, err := convertYamlToolToApiTool(yamlTool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ft, ok := result.(agent_api.FunctionTool)
	if !ok {
		t.Fatalf("expected agent_api.FunctionTool, got %T", result)
	}
	if ft.Tool.Type != agent_api.ToolTypeFunction {
		t.Errorf("type = %q, want %q", ft.Tool.Type, agent_api.ToolTypeFunction)
	}
	if ft.Name != "add" {
		t.Errorf("name = %q, want %q", ft.Name, "add")
	}
	if ft.Description == nil || *ft.Description != desc {
		t.Errorf("description mismatch")
	}
	if ft.Strict == nil || !*ft.Strict {
		t.Error("strict should be true")
	}
}

func TestConvertYamlToolToApiTool_FunctionNilDescription(t *testing.T) {
	t.Parallel()
	yamlTool := FunctionTool{
		Tool: Tool{
			Name: "noop",
			Kind: ToolKindFunction,
		},
		Parameters: PropertySchema{},
	}

	result, err := convertYamlToolToApiTool(yamlTool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ft := result.(agent_api.FunctionTool)
	if ft.Description != nil {
		t.Errorf("expected nil description, got %v", ft.Description)
	}
	if ft.Strict != nil {
		t.Errorf("expected nil strict, got %v", ft.Strict)
	}
}

func TestConvertYamlToolToApiTool_WebSearch(t *testing.T) {
	t.Parallel()
	yamlTool := WebSearchTool{
		Tool: Tool{Name: "websearch", Kind: ToolKindWebSearch},
		Options: map[string]any{
			"searchContextSize": "high",
		},
	}

	result, err := convertYamlToolToApiTool(yamlTool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ws, ok := result.(agent_api.WebSearchPreviewTool)
	if !ok {
		t.Fatalf("expected agent_api.WebSearchPreviewTool, got %T", result)
	}
	if ws.Tool.Type != agent_api.ToolTypeWebSearchPreview {
		t.Errorf("type = %q, want %q", ws.Tool.Type, agent_api.ToolTypeWebSearchPreview)
	}
	if ws.SearchContextSize == nil || *ws.SearchContextSize != "high" {
		t.Errorf("searchContextSize mismatch")
	}
}

func TestConvertYamlToolToApiTool_WebSearchNoOptions(t *testing.T) {
	t.Parallel()
	yamlTool := WebSearchTool{
		Tool: Tool{Name: "ws", Kind: ToolKindWebSearch},
	}

	result, err := convertYamlToolToApiTool(yamlTool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ws := result.(agent_api.WebSearchPreviewTool)
	if ws.UserLocation != nil {
		t.Error("expected nil UserLocation")
	}
	if ws.SearchContextSize != nil {
		t.Error("expected nil SearchContextSize")
	}
}

func TestConvertYamlToolToApiTool_BingGrounding(t *testing.T) {
	t.Parallel()
	bgParams := agent_api.BingGroundingSearchToolParameters{
		ProjectConnections: agent_api.ToolProjectConnectionList{
			ProjectConnections: []agent_api.ToolProjectConnection{{ID: "conn-1"}},
		},
	}
	yamlTool := BingGroundingTool{
		Tool: Tool{Name: "bing", Kind: ToolKindBingGrounding},
		Options: map[string]any{
			"bingGrounding": bgParams,
		},
	}

	result, err := convertYamlToolToApiTool(yamlTool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	bg, ok := result.(agent_api.BingGroundingAgentTool)
	if !ok {
		t.Fatalf("expected agent_api.BingGroundingAgentTool, got %T", result)
	}
	if bg.Tool.Type != agent_api.ToolTypeBingGrounding {
		t.Errorf("type = %q, want %q", bg.Tool.Type, agent_api.ToolTypeBingGrounding)
	}
	if len(bg.BingGrounding.ProjectConnections.ProjectConnections) != 1 {
		t.Errorf("expected 1 project connection")
	}
}

func TestConvertYamlToolToApiTool_BingGroundingNoOptions(t *testing.T) {
	t.Parallel()
	yamlTool := BingGroundingTool{
		Tool: Tool{Name: "bing", Kind: ToolKindBingGrounding},
	}

	result, err := convertYamlToolToApiTool(yamlTool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	bg := result.(agent_api.BingGroundingAgentTool)
	if bg.Tool.Type != agent_api.ToolTypeBingGrounding {
		t.Errorf("type = %q", bg.Tool.Type)
	}
}

func TestConvertYamlToolToApiTool_FileSearch(t *testing.T) {
	t.Parallel()
	ranker := "default-2024-11-15"
	threshold := 0.8
	maxResults := 10
	yamlTool := FileSearchTool{
		Tool:               Tool{Name: "fs", Kind: ToolKindFileSearch},
		VectorStoreIds:     []string{"vs-1", "vs-2"},
		MaximumResultCount: &maxResults,
		Ranker:             &ranker,
		ScoreThreshold:     &threshold,
		Options: map[string]any{
			"filters": map[string]any{"type": "eq", "key": "status", "value": "active"},
		},
	}

	result, err := convertYamlToolToApiTool(yamlTool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fs, ok := result.(agent_api.FileSearchTool)
	if !ok {
		t.Fatalf("expected agent_api.FileSearchTool, got %T", result)
	}
	if fs.Tool.Type != agent_api.ToolTypeFileSearch {
		t.Errorf("type = %q", fs.Tool.Type)
	}
	if len(fs.VectorStoreIds) != 2 {
		t.Errorf("expected 2 vector store ids, got %d", len(fs.VectorStoreIds))
	}
	if fs.MaxNumResults == nil || *fs.MaxNumResults != 10 {
		t.Errorf("MaxNumResults mismatch")
	}
	if fs.RankingOptions == nil {
		t.Fatal("expected non-nil RankingOptions")
	}
	if fs.RankingOptions.Ranker == nil || *fs.RankingOptions.Ranker != ranker {
		t.Errorf("ranker mismatch")
	}
	if fs.RankingOptions.ScoreThreshold == nil || *fs.RankingOptions.ScoreThreshold != float32(threshold) {
		t.Errorf("score threshold mismatch")
	}
	if fs.Filters == nil {
		t.Error("expected filters to be set")
	}
}

func TestConvertYamlToolToApiTool_FileSearchMinimal(t *testing.T) {
	t.Parallel()
	yamlTool := FileSearchTool{
		Tool:           Tool{Name: "fs", Kind: ToolKindFileSearch},
		VectorStoreIds: []string{"vs-1"},
	}

	result, err := convertYamlToolToApiTool(yamlTool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fs := result.(agent_api.FileSearchTool)
	if fs.MaxNumResults != nil {
		t.Error("expected nil MaxNumResults")
	}
	if fs.RankingOptions != nil {
		t.Error("expected nil RankingOptions when ranker and threshold are nil")
	}
}

func TestConvertYamlToolToApiTool_FileSearchOverflow(t *testing.T) {
	t.Parallel()
	overflow := math.MaxInt32 + 1
	yamlTool := FileSearchTool{
		Tool:               Tool{Name: "fs", Kind: ToolKindFileSearch},
		VectorStoreIds:     []string{"vs-1"},
		MaximumResultCount: &overflow,
	}

	_, err := convertYamlToolToApiTool(yamlTool)
	if err == nil {
		t.Fatal("expected error for int32 overflow")
	}
	if !strings.Contains(err.Error(), "overflow") {
		t.Errorf("error should mention overflow, got: %s", err.Error())
	}
}

func TestConvertYamlToolToApiTool_MCP(t *testing.T) {
	t.Parallel()
	yamlTool := McpTool{
		Tool:       Tool{Name: "mcp-server", Kind: ToolKindMcp},
		ServerName: "my-mcp-server",
		Options: map[string]any{
			"serverUrl":           "https://mcp.example.com",
			"headers":             map[string]string{"Authorization": "Bearer tok"},
			"allowedTools":        []string{"tool_a", "tool_b"},
			"requireApproval":     "always",
			"projectConnectionId": "conn-123",
		},
	}

	result, err := convertYamlToolToApiTool(yamlTool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mcp, ok := result.(agent_api.MCPTool)
	if !ok {
		t.Fatalf("expected agent_api.MCPTool, got %T", result)
	}
	if mcp.Tool.Type != agent_api.ToolTypeMCP {
		t.Errorf("type = %q", mcp.Tool.Type)
	}
	if mcp.ServerLabel != "my-mcp-server" {
		t.Errorf("ServerLabel = %q", mcp.ServerLabel)
	}
	if mcp.ServerURL != "https://mcp.example.com" {
		t.Errorf("ServerURL = %q", mcp.ServerURL)
	}
	if mcp.Headers["Authorization"] != "Bearer tok" {
		t.Errorf("headers mismatch")
	}
	if mcp.ProjectConnectionID == nil || *mcp.ProjectConnectionID != "conn-123" {
		t.Errorf("ProjectConnectionID mismatch")
	}
}

func TestConvertYamlToolToApiTool_MCPNoOptions(t *testing.T) {
	t.Parallel()
	yamlTool := McpTool{
		Tool:       Tool{Name: "mcp", Kind: ToolKindMcp},
		ServerName: "srv",
	}

	result, err := convertYamlToolToApiTool(yamlTool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mcp := result.(agent_api.MCPTool)
	if mcp.ServerURL != "" {
		t.Errorf("expected empty ServerURL, got %q", mcp.ServerURL)
	}
	if mcp.ProjectConnectionID != nil {
		t.Error("expected nil ProjectConnectionID")
	}
}

func TestConvertYamlToolToApiTool_OpenApi(t *testing.T) {
	t.Parallel()
	openApiDef := agent_api.OpenApiFunctionDefinition{
		Name: "petstore",
		Auth: agent_api.OpenApiAuthDetails{Type: agent_api.OpenApiAuthTypeAnonymous},
	}
	yamlTool := OpenApiTool{
		Tool: Tool{Name: "petstore", Kind: ToolKindOpenApi},
		Options: map[string]any{
			"openapi": openApiDef,
		},
	}

	result, err := convertYamlToolToApiTool(yamlTool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	oa, ok := result.(agent_api.OpenApiAgentTool)
	if !ok {
		t.Fatalf("expected agent_api.OpenApiAgentTool, got %T", result)
	}
	if oa.Tool.Type != agent_api.ToolTypeOpenAPI {
		t.Errorf("type = %q", oa.Tool.Type)
	}
	if oa.OpenAPI.Name != "petstore" {
		t.Errorf("OpenAPI.Name = %q", oa.OpenAPI.Name)
	}
}

func TestConvertYamlToolToApiTool_OpenApiNoOptions(t *testing.T) {
	t.Parallel()
	yamlTool := OpenApiTool{
		Tool: Tool{Name: "api", Kind: ToolKindOpenApi},
	}

	result, err := convertYamlToolToApiTool(yamlTool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	oa := result.(agent_api.OpenApiAgentTool)
	if oa.Tool.Type != agent_api.ToolTypeOpenAPI {
		t.Errorf("type = %q", oa.Tool.Type)
	}
}

func TestConvertYamlToolToApiTool_CodeInterpreter(t *testing.T) {
	t.Parallel()
	yamlTool := CodeInterpreterTool{
		Tool: Tool{Name: "ci", Kind: ToolKindCodeInterpreter},
		Options: map[string]any{
			"container": "auto",
		},
	}

	result, err := convertYamlToolToApiTool(yamlTool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ci, ok := result.(agent_api.CodeInterpreterTool)
	if !ok {
		t.Fatalf("expected agent_api.CodeInterpreterTool, got %T", result)
	}
	if ci.Tool.Type != agent_api.ToolTypeCodeInterpreter {
		t.Errorf("type = %q", ci.Tool.Type)
	}
	if ci.Container != "auto" {
		t.Errorf("Container = %v", ci.Container)
	}
}

func TestConvertYamlToolToApiTool_CodeInterpreterNoOptions(t *testing.T) {
	t.Parallel()
	yamlTool := CodeInterpreterTool{
		Tool: Tool{Name: "ci", Kind: ToolKindCodeInterpreter},
	}

	result, err := convertYamlToolToApiTool(yamlTool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ci := result.(agent_api.CodeInterpreterTool)
	if ci.Container != nil {
		t.Errorf("expected nil Container, got %v", ci.Container)
	}
}

// ---------------------------------------------------------------------------
// convertYamlToolsToApiTools
// ---------------------------------------------------------------------------

func TestConvertYamlToolsToApiTools_MixedTools(t *testing.T) {
	t.Parallel()
	yamlTools := []any{
		FunctionTool{Tool: Tool{Name: "fn1", Kind: ToolKindFunction}, Parameters: PropertySchema{}},
		WebSearchTool{Tool: Tool{Name: "ws", Kind: ToolKindWebSearch}},
		CodeInterpreterTool{Tool: Tool{Name: "ci", Kind: ToolKindCodeInterpreter}},
	}

	result := convertYamlToolsToApiTools(yamlTools)
	if len(result) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(result))
	}

	if _, ok := result[0].(agent_api.FunctionTool); !ok {
		t.Errorf("tool[0] should be FunctionTool, got %T", result[0])
	}
	if _, ok := result[1].(agent_api.WebSearchPreviewTool); !ok {
		t.Errorf("tool[1] should be WebSearchPreviewTool, got %T", result[1])
	}
	if _, ok := result[2].(agent_api.CodeInterpreterTool); !ok {
		t.Errorf("tool[2] should be CodeInterpreterTool, got %T", result[2])
	}
}

func TestConvertYamlToolsToApiTools_SkipsUnsupported(t *testing.T) {
	t.Parallel()
	yamlTools := []any{
		FunctionTool{Tool: Tool{Name: "fn1", Kind: ToolKindFunction}, Parameters: PropertySchema{}},
		"unsupported-string-tool",
		WebSearchTool{Tool: Tool{Name: "ws", Kind: ToolKindWebSearch}},
	}

	result := convertYamlToolsToApiTools(yamlTools)
	if len(result) != 2 {
		t.Fatalf("expected 2 tools (unsupported skipped), got %d", len(result))
	}
}

func TestConvertYamlToolsToApiTools_Empty(t *testing.T) {
	t.Parallel()
	result := convertYamlToolsToApiTools([]any{})
	if result != nil {
		t.Errorf("expected nil for empty input, got %v", result)
	}
}

// ---------------------------------------------------------------------------
// createAgentAPIRequest (common fields)
// ---------------------------------------------------------------------------

func TestCreateAgentAPIRequest_AllFields(t *testing.T) {
	t.Parallel()
	desc := "A helpful agent"
	meta := map[string]any{
		"authors": []any{"Alice", "Bob"},
		"version": "1.0",
	}
	agentDef := AgentDefinition{
		Kind:        AgentKindPrompt,
		Name:        "my-agent",
		Description: &desc,
		Metadata:    &meta,
	}

	req, err := createAgentAPIRequest(agentDef, "placeholder-definition")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Name != "my-agent" {
		t.Errorf("Name = %q, want %q", req.Name, "my-agent")
	}
	if req.Description == nil || *req.Description != desc {
		t.Errorf("Description mismatch")
	}
	if req.Metadata["authors"] != "Alice,Bob" {
		t.Errorf("authors = %q, want %q", req.Metadata["authors"], "Alice,Bob")
	}
	if req.Metadata["version"] != "1.0" {
		t.Errorf("version metadata = %q", req.Metadata["version"])
	}
	if req.Definition != "placeholder-definition" {
		t.Errorf("Definition mismatch")
	}
}

func TestCreateAgentAPIRequest_DefaultName(t *testing.T) {
	t.Parallel()
	agentDef := AgentDefinition{Kind: AgentKindPrompt}

	req, err := createAgentAPIRequest(agentDef, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Name != "unspecified-agent-name" {
		t.Errorf("Name = %q, want %q", req.Name, "unspecified-agent-name")
	}
}

func TestCreateAgentAPIRequest_NilMetadata(t *testing.T) {
	t.Parallel()
	agentDef := AgentDefinition{Kind: AgentKindPrompt, Name: "test"}

	req, err := createAgentAPIRequest(agentDef, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Metadata != nil {
		t.Errorf("expected nil Metadata, got %v", req.Metadata)
	}
}

func TestCreateAgentAPIRequest_EmptyDescription(t *testing.T) {
	t.Parallel()
	empty := ""
	agentDef := AgentDefinition{
		Kind:        AgentKindPrompt,
		Name:        "test",
		Description: &empty,
	}

	req, err := createAgentAPIRequest(agentDef, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Description != nil {
		t.Errorf("expected nil Description for empty string, got %v", req.Description)
	}
}

func TestCreateAgentAPIRequest_MetadataWithNonStringValues(t *testing.T) {
	t.Parallel()
	meta := map[string]any{
		"name":    "test",
		"numeric": 42, // non-string value should be skipped
	}
	agentDef := AgentDefinition{
		Kind:     AgentKindPrompt,
		Name:     "test",
		Metadata: &meta,
	}

	req, err := createAgentAPIRequest(agentDef, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Metadata["name"] != "test" {
		t.Errorf("string metadata missing")
	}
	if _, exists := req.Metadata["numeric"]; exists {
		t.Errorf("non-string metadata should be skipped")
	}
}

func TestCreateAgentAPIRequest_AuthorsSingleAuthor(t *testing.T) {
	t.Parallel()
	meta := map[string]any{
		"authors": []any{"Solo"},
	}
	agentDef := AgentDefinition{
		Kind:     AgentKindPrompt,
		Name:     "test",
		Metadata: &meta,
	}

	req, err := createAgentAPIRequest(agentDef, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Metadata["authors"] != "Solo" {
		t.Errorf("authors = %q, want %q", req.Metadata["authors"], "Solo")
	}
}

// ---------------------------------------------------------------------------
// CreatePromptAgentAPIRequest
// ---------------------------------------------------------------------------

func TestCreatePromptAgentAPIRequest_FullConfig(t *testing.T) {
	t.Parallel()
	desc := "prompt agent"
	instructions := "You are a helpful assistant."
	temp := 0.7
	topP := 0.9

	agent := PromptAgent{
		AgentDefinition: AgentDefinition{
			Kind:        AgentKindPrompt,
			Name:        "my-prompt-agent",
			Description: &desc,
		},
		Model: Model{
			Id: "gpt-4o",
			Options: &ModelOptions{
				Temperature: &temp,
				TopP:        &topP,
			},
		},
		Instructions: &instructions,
		Tools: &[]any{
			FunctionTool{
				Tool:       Tool{Name: "calc", Kind: ToolKindFunction},
				Parameters: PropertySchema{},
			},
		},
	}

	req, err := CreatePromptAgentAPIRequest(agent, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Name != "my-prompt-agent" {
		t.Errorf("Name = %q", req.Name)
	}
	if req.Description == nil || *req.Description != desc {
		t.Errorf("Description mismatch")
	}

	promptDef, ok := req.Definition.(agent_api.PromptAgentDefinition)
	if !ok {
		t.Fatalf("Definition should be PromptAgentDefinition, got %T", req.Definition)
	}
	if promptDef.Kind != agent_api.AgentKindPrompt {
		t.Errorf("Kind = %q", promptDef.Kind)
	}
	if promptDef.Model != "gpt-4o" {
		t.Errorf("Model = %q", promptDef.Model)
	}
	if promptDef.Instructions == nil || *promptDef.Instructions != instructions {
		t.Errorf("Instructions mismatch")
	}
	if promptDef.Temperature == nil || *promptDef.Temperature != float32(0.7) {
		t.Errorf("Temperature mismatch")
	}
	if promptDef.TopP == nil || *promptDef.TopP != float32(0.9) {
		t.Errorf("TopP mismatch")
	}
	if len(promptDef.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(promptDef.Tools))
	}
}

func TestCreatePromptAgentAPIRequest_MissingModelId(t *testing.T) {
	t.Parallel()
	agent := PromptAgent{
		AgentDefinition: AgentDefinition{
			Kind: AgentKindPrompt,
			Name: "bad-agent",
		},
		Model: Model{Id: ""},
		Tools: &[]any{},
	}

	_, err := CreatePromptAgentAPIRequest(agent, nil)
	if err == nil {
		t.Fatal("expected error for missing model.id")
	}
	if !strings.Contains(err.Error(), "model.id") {
		t.Errorf("error should mention model.id, got: %s", err.Error())
	}
}

func TestCreatePromptAgentAPIRequest_NoOptions(t *testing.T) {
	t.Parallel()
	agent := PromptAgent{
		AgentDefinition: AgentDefinition{
			Kind: AgentKindPrompt,
			Name: "simple-agent",
		},
		Model: Model{Id: "gpt-4o-mini"},
		Tools: &[]any{},
	}

	req, err := CreatePromptAgentAPIRequest(agent, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	promptDef := req.Definition.(agent_api.PromptAgentDefinition)
	if promptDef.Temperature != nil {
		t.Errorf("expected nil Temperature, got %v", *promptDef.Temperature)
	}
	if promptDef.TopP != nil {
		t.Errorf("expected nil TopP, got %v", *promptDef.TopP)
	}
	if promptDef.Instructions != nil {
		t.Errorf("expected nil Instructions")
	}
}

func TestCreatePromptAgentAPIRequest_NilToolsSlice(t *testing.T) {
	t.Parallel()
	emptyTools := []any{}
	agent := PromptAgent{
		AgentDefinition: AgentDefinition{
			Kind: AgentKindPrompt,
			Name: "no-tools",
		},
		Model: Model{Id: "gpt-4o"},
		Tools: &emptyTools,
	}

	req, err := CreatePromptAgentAPIRequest(agent, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	promptDef := req.Definition.(agent_api.PromptAgentDefinition)
	if promptDef.Tools != nil {
		t.Errorf("expected nil Tools for empty input, got %v", promptDef.Tools)
	}
}

// ---------------------------------------------------------------------------
// CreateHostedAgentAPIRequest
// ---------------------------------------------------------------------------

func TestCreateHostedAgentAPIRequest_FullConfig(t *testing.T) {
	t.Parallel()
	desc := "hosted agent"
	agent := ContainerAgent{
		AgentDefinition: AgentDefinition{
			Kind:        AgentKindHosted,
			Name:        "my-hosted-agent",
			Description: &desc,
		},
		Protocols: []ProtocolVersionRecord{
			{Protocol: "responses", Version: "2.0.0"},
			{Protocol: "invocations", Version: "1.0.0"},
		},
	}

	buildConfig := &AgentBuildConfig{
		ImageURL:             "myregistry.azurecr.io/agent:v1",
		CPU:                  "4",
		Memory:               "8Gi",
		EnvironmentVariables: map[string]string{"ENV1": "val1"},
	}

	req, err := CreateHostedAgentAPIRequest(agent, buildConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Name != "my-hosted-agent" {
		t.Errorf("Name = %q", req.Name)
	}
	if req.Description == nil || *req.Description != desc {
		t.Errorf("Description mismatch")
	}

	imgDef, ok := req.Definition.(agent_api.ImageBasedHostedAgentDefinition)
	if !ok {
		t.Fatalf("expected ImageBasedHostedAgentDefinition, got %T", req.Definition)
	}
	if imgDef.Kind != agent_api.AgentKindHosted {
		t.Errorf("Kind = %q", imgDef.Kind)
	}
	if imgDef.Image != "myregistry.azurecr.io/agent:v1" {
		t.Errorf("Image = %q", imgDef.Image)
	}
	if imgDef.CPU != "4" {
		t.Errorf("CPU = %q", imgDef.CPU)
	}
	if imgDef.Memory != "8Gi" {
		t.Errorf("Memory = %q", imgDef.Memory)
	}
	if imgDef.EnvironmentVariables["ENV1"] != "val1" {
		t.Error("env var missing")
	}

	// Verify protocol versions
	if len(imgDef.ContainerProtocolVersions) != 2 {
		t.Fatalf("expected 2 protocol versions, got %d", len(imgDef.ContainerProtocolVersions))
	}
	if imgDef.ContainerProtocolVersions[0].Protocol != "responses" {
		t.Errorf("protocol[0] = %q", imgDef.ContainerProtocolVersions[0].Protocol)
	}
	if imgDef.ContainerProtocolVersions[0].Version != "2.0.0" {
		t.Errorf("version[0] = %q", imgDef.ContainerProtocolVersions[0].Version)
	}
}

func TestCreateHostedAgentAPIRequest_DefaultProtocols(t *testing.T) {
	t.Parallel()
	agent := ContainerAgent{
		AgentDefinition: AgentDefinition{
			Kind: AgentKindHosted,
			Name: "default-protocols",
		},
	}
	buildConfig := &AgentBuildConfig{ImageURL: "img:latest"}

	req, err := CreateHostedAgentAPIRequest(agent, buildConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	imgDef := req.Definition.(agent_api.ImageBasedHostedAgentDefinition)
	if len(imgDef.ContainerProtocolVersions) != 1 {
		t.Fatalf("expected 1 default protocol, got %d", len(imgDef.ContainerProtocolVersions))
	}
	if imgDef.ContainerProtocolVersions[0].Protocol != agent_api.AgentProtocolResponses {
		t.Errorf("default protocol = %q", imgDef.ContainerProtocolVersions[0].Protocol)
	}
	if imgDef.ContainerProtocolVersions[0].Version != "v1" {
		t.Errorf("default version = %q", imgDef.ContainerProtocolVersions[0].Version)
	}
}

func TestCreateHostedAgentAPIRequest_DefaultCPUAndMemory(t *testing.T) {
	t.Parallel()
	agent := ContainerAgent{
		AgentDefinition: AgentDefinition{
			Kind: AgentKindHosted,
			Name: "defaults",
		},
	}
	buildConfig := &AgentBuildConfig{ImageURL: "img:latest"}

	req, err := CreateHostedAgentAPIRequest(agent, buildConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	imgDef := req.Definition.(agent_api.ImageBasedHostedAgentDefinition)
	if imgDef.CPU != "1" {
		t.Errorf("default CPU = %q, want %q", imgDef.CPU, "1")
	}
	if imgDef.Memory != "2Gi" {
		t.Errorf("default Memory = %q, want %q", imgDef.Memory, "2Gi")
	}
}

func TestCreateHostedAgentAPIRequest_MissingImageURL(t *testing.T) {
	t.Parallel()
	agent := ContainerAgent{
		AgentDefinition: AgentDefinition{
			Kind: AgentKindHosted,
			Name: "no-image",
		},
	}

	_, err := CreateHostedAgentAPIRequest(agent, &AgentBuildConfig{})
	if err == nil {
		t.Fatal("expected error for missing image URL")
	}
	if !strings.Contains(err.Error(), "image URL") {
		t.Errorf("error should mention image URL, got: %s", err.Error())
	}
}

func TestCreateHostedAgentAPIRequest_NilBuildConfig(t *testing.T) {
	t.Parallel()
	agent := ContainerAgent{
		AgentDefinition: AgentDefinition{
			Kind: AgentKindHosted,
			Name: "nil-config",
		},
	}

	_, err := CreateHostedAgentAPIRequest(agent, nil)
	if err == nil {
		t.Fatal("expected error for nil build config (no image)")
	}
}

// ---------------------------------------------------------------------------
// CreateAgentAPIRequestFromDefinition (routing)
// ---------------------------------------------------------------------------

func TestCreateAgentAPIRequestFromDefinition_PromptAgent(t *testing.T) {
	t.Parallel()
	agent := PromptAgent{
		AgentDefinition: AgentDefinition{
			Kind: AgentKindPrompt,
			Name: "prompt-routed",
		},
		Model: Model{Id: "gpt-4o"},
		Tools: &[]any{},
	}

	req, err := CreateAgentAPIRequestFromDefinition(agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Name != "prompt-routed" {
		t.Errorf("Name = %q", req.Name)
	}

	_, ok := req.Definition.(agent_api.PromptAgentDefinition)
	if !ok {
		t.Fatalf("expected PromptAgentDefinition, got %T", req.Definition)
	}
}

func TestCreateAgentAPIRequestFromDefinition_HostedAgent(t *testing.T) {
	t.Parallel()
	agent := ContainerAgent{
		AgentDefinition: AgentDefinition{
			Kind: AgentKindHosted,
			Name: "hosted-routed",
		},
	}

	req, err := CreateAgentAPIRequestFromDefinition(agent, WithImageURL("img:latest"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Name != "hosted-routed" {
		t.Errorf("Name = %q", req.Name)
	}

	_, ok := req.Definition.(agent_api.ImageBasedHostedAgentDefinition)
	if !ok {
		t.Fatalf("expected ImageBasedHostedAgentDefinition, got %T", req.Definition)
	}
}

func TestCreateAgentAPIRequestFromDefinition_UnsupportedKind(t *testing.T) {
	t.Parallel()
	agent := AgentDefinition{
		Kind: "unknown",
		Name: "bad-kind",
	}

	_, err := CreateAgentAPIRequestFromDefinition(agent)
	if err == nil {
		t.Fatal("expected error for unsupported kind")
	}
	if !strings.Contains(err.Error(), "unsupported agent kind") {
		t.Errorf("error should mention unsupported agent kind, got: %s", err.Error())
	}
}

func TestCreateAgentAPIRequestFromDefinition_HostedWithBuildOptions(t *testing.T) {
	t.Parallel()
	agent := ContainerAgent{
		AgentDefinition: AgentDefinition{
			Kind: AgentKindHosted,
			Name: "hosted-opts",
		},
		Protocols: []ProtocolVersionRecord{
			{Protocol: "responses", Version: "1.0.0"},
		},
	}

	req, err := CreateAgentAPIRequestFromDefinition(agent,
		WithImageURL("myimg:v2"),
		WithCPU("2"),
		WithMemory("4Gi"),
		WithEnvironmentVariable("FOO", "bar"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	imgDef := req.Definition.(agent_api.ImageBasedHostedAgentDefinition)
	if imgDef.Image != "myimg:v2" {
		t.Errorf("Image = %q", imgDef.Image)
	}
	if imgDef.CPU != "2" {
		t.Errorf("CPU = %q", imgDef.CPU)
	}
	if imgDef.Memory != "4Gi" {
		t.Errorf("Memory = %q", imgDef.Memory)
	}
	if imgDef.EnvironmentVariables["FOO"] != "bar" {
		t.Errorf("env var FOO missing or wrong")
	}
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package registry_api

import (
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
)

// ptr is a generic helper that returns a pointer to the given value.
// ---------------------------------------------------------------------------
// ConvertToolToYaml
// ---------------------------------------------------------------------------

func TestConvertToolToYaml(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     any
		wantErr   bool
		errSubstr string
		validate  func(t *testing.T, got any)
	}{
		{
			name:      "nil tool returns error",
			input:     nil,
			wantErr:   true,
			errSubstr: "tool cannot be nil",
		},
		{
			name:      "unsupported type returns error",
			input:     "not-a-tool",
			wantErr:   true,
			errSubstr: "unsupported tool type",
		},
		{
			name: "FunctionTool",
			input: agent_api.FunctionTool{
				Tool:        agent_api.Tool{Type: "function"},
				Name:        "my_func",
				Description: new("a helper function"),
				Parameters:  nil,
				Strict:      new(true),
			},
			validate: func(t *testing.T, got any) {
				ft, ok := got.(agent_yaml.FunctionTool)
				if !ok {
					t.Fatalf("expected agent_yaml.FunctionTool, got %T", got)
				}
				if ft.Tool.Kind != agent_yaml.ToolKindFunction {
					t.Errorf("Kind = %q, want %q", ft.Tool.Kind, agent_yaml.ToolKindFunction)
				}
				if ft.Tool.Name != "my_func" {
					t.Errorf("Name = %q, want %q", ft.Tool.Name, "my_func")
				}
				if ft.Tool.Description == nil || *ft.Tool.Description != "a helper function" {
					t.Errorf("Description mismatch")
				}
				if ft.Strict == nil || *ft.Strict != true {
					t.Errorf("Strict = %v, want true", ft.Strict)
				}
			},
		},
		{
			name: "WebSearchPreviewTool with options",
			input: agent_api.WebSearchPreviewTool{
				Tool:              agent_api.Tool{Type: "web_search_preview"},
				UserLocation:      &agent_api.Location{Type: "approximate"},
				SearchContextSize: new("medium"),
			},
			validate: func(t *testing.T, got any) {
				ws, ok := got.(agent_yaml.WebSearchTool)
				if !ok {
					t.Fatalf("expected agent_yaml.WebSearchTool, got %T", got)
				}
				if ws.Tool.Kind != agent_yaml.ToolKindWebSearch {
					t.Errorf("Kind = %q, want %q", ws.Tool.Kind, agent_yaml.ToolKindWebSearch)
				}
				if ws.Tool.Name != "web_search_preview" {
					t.Errorf("Name = %q, want %q", ws.Tool.Name, "web_search_preview")
				}
				if ws.Options == nil {
					t.Fatal("Options is nil")
				}
				if _, exists := ws.Options["userLocation"]; !exists {
					t.Error("expected userLocation in Options")
				}
				if ws.Options["searchContextSize"] != "medium" {
					t.Errorf("searchContextSize = %v, want %q", ws.Options["searchContextSize"], "medium")
				}
			},
		},
		{
			name: "WebSearchPreviewTool without options",
			input: agent_api.WebSearchPreviewTool{
				Tool: agent_api.Tool{Type: "web_search_preview"},
			},
			validate: func(t *testing.T, got any) {
				ws, ok := got.(agent_yaml.WebSearchTool)
				if !ok {
					t.Fatalf("expected agent_yaml.WebSearchTool, got %T", got)
				}
				// Options map is always created but should be empty
				if len(ws.Options) != 0 {
					t.Errorf("expected empty Options, got %v", ws.Options)
				}
			},
		},
		{
			name: "BingGroundingAgentTool",
			input: agent_api.BingGroundingAgentTool{
				Tool: agent_api.Tool{Type: "bing_grounding"},
				BingGrounding: agent_api.BingGroundingSearchToolParameters{
					ProjectConnections: agent_api.ToolProjectConnectionList{
						ProjectConnections: []agent_api.ToolProjectConnection{
							{ID: "conn-1"},
						},
					},
				},
			},
			validate: func(t *testing.T, got any) {
				bg, ok := got.(agent_yaml.BingGroundingTool)
				if !ok {
					t.Fatalf("expected agent_yaml.BingGroundingTool, got %T", got)
				}
				if bg.Tool.Kind != agent_yaml.ToolKindBingGrounding {
					t.Errorf("Kind = %q, want %q", bg.Tool.Kind, agent_yaml.ToolKindBingGrounding)
				}
				if bg.Tool.Name != "bing_grounding" {
					t.Errorf("Name = %q, want %q", bg.Tool.Name, "bing_grounding")
				}
				if bg.Options == nil {
					t.Fatal("Options is nil")
				}
				if _, exists := bg.Options["bingGrounding"]; !exists {
					t.Error("expected bingGrounding in Options")
				}
			},
		},
		{
			name: "FileSearchTool with ranking options",
			input: agent_api.FileSearchTool{
				Tool:           agent_api.Tool{Type: "file_search"},
				VectorStoreIds: []string{"vs-1", "vs-2"},
				MaxNumResults:  new(int32(10)),
				RankingOptions: &agent_api.RankingOptions{
					Ranker:         new("auto"),
					ScoreThreshold: new(float32(0.5)),
				},
			},
			validate: func(t *testing.T, got any) {
				fs, ok := got.(agent_yaml.FileSearchTool)
				if !ok {
					t.Fatalf("expected agent_yaml.FileSearchTool, got %T", got)
				}
				if fs.Tool.Kind != agent_yaml.ToolKindFileSearch {
					t.Errorf("Kind = %q, want %q", fs.Tool.Kind, agent_yaml.ToolKindFileSearch)
				}
				if len(fs.VectorStoreIds) != 2 || fs.VectorStoreIds[0] != "vs-1" {
					t.Errorf("VectorStoreIds = %v, want [vs-1 vs-2]", fs.VectorStoreIds)
				}
				if fs.MaximumResultCount == nil || *fs.MaximumResultCount != 10 {
					t.Errorf("MaximumResultCount = %v, want 10", fs.MaximumResultCount)
				}
				if fs.Ranker == nil || *fs.Ranker != "auto" {
					t.Errorf("Ranker = %v, want auto", fs.Ranker)
				}
				if fs.ScoreThreshold == nil || *fs.ScoreThreshold != float64(float32(0.5)) {
					t.Errorf("ScoreThreshold = %v, want 0.5", fs.ScoreThreshold)
				}
			},
		},
		{
			name: "FileSearchTool without ranking options",
			input: agent_api.FileSearchTool{
				Tool:           agent_api.Tool{Type: "file_search"},
				VectorStoreIds: []string{"vs-1"},
			},
			validate: func(t *testing.T, got any) {
				fs, ok := got.(agent_yaml.FileSearchTool)
				if !ok {
					t.Fatalf("expected agent_yaml.FileSearchTool, got %T", got)
				}
				if fs.Ranker != nil {
					t.Errorf("Ranker = %v, want nil", fs.Ranker)
				}
				if fs.ScoreThreshold != nil {
					t.Errorf("ScoreThreshold = %v, want nil", fs.ScoreThreshold)
				}
				if fs.MaximumResultCount != nil {
					t.Errorf("MaximumResultCount = %v, want nil", fs.MaximumResultCount)
				}
			},
		},
		{
			name: "MCPTool with all fields",
			input: agent_api.MCPTool{
				Tool:                agent_api.Tool{Type: "mcp"},
				ServerLabel:         "my-server",
				ServerURL:           "https://example.com",
				Headers:             map[string]string{"x-key": "val"},
				ProjectConnectionID: new("conn-1"),
			},
			validate: func(t *testing.T, got any) {
				mcp, ok := got.(agent_yaml.McpTool)
				if !ok {
					t.Fatalf("expected agent_yaml.McpTool, got %T", got)
				}
				if mcp.Tool.Kind != agent_yaml.ToolKindMcp {
					t.Errorf("Kind = %q, want %q", mcp.Tool.Kind, agent_yaml.ToolKindMcp)
				}
				if mcp.ServerName != "my-server" {
					t.Errorf("ServerName = %q, want %q", mcp.ServerName, "my-server")
				}
				if mcp.Options["serverUrl"] != "https://example.com" {
					t.Errorf("serverUrl = %v, want %q", mcp.Options["serverUrl"], "https://example.com")
				}
				if mcp.Options["projectConnectionId"] != "conn-1" {
					t.Errorf("projectConnectionId = %v, want %q", mcp.Options["projectConnectionId"], "conn-1")
				}
				headers, ok := mcp.Options["headers"].(map[string]string)
				if !ok {
					t.Fatalf("expected headers map[string]string, got %T", mcp.Options["headers"])
				}
				if headers["x-key"] != "val" {
					t.Errorf("header x-key = %q, want %q", headers["x-key"], "val")
				}
			},
		},
		{
			name: "MCPTool minimal",
			input: agent_api.MCPTool{
				Tool:        agent_api.Tool{Type: "mcp"},
				ServerLabel: "minimal-server",
			},
			validate: func(t *testing.T, got any) {
				mcp, ok := got.(agent_yaml.McpTool)
				if !ok {
					t.Fatalf("expected agent_yaml.McpTool, got %T", got)
				}
				if mcp.ServerName != "minimal-server" {
					t.Errorf("ServerName = %q, want %q", mcp.ServerName, "minimal-server")
				}
				// serverUrl should not appear when empty string
				if _, exists := mcp.Options["serverUrl"]; exists {
					t.Error("expected serverUrl to be absent for empty ServerURL")
				}
				if _, exists := mcp.Options["headers"]; exists {
					t.Error("expected headers to be absent when nil")
				}
				if _, exists := mcp.Options["projectConnectionId"]; exists {
					t.Error("expected projectConnectionId to be absent when nil")
				}
			},
		},
		{
			name: "OpenApiAgentTool",
			input: agent_api.OpenApiAgentTool{
				Tool: agent_api.Tool{Type: "openapi"},
				OpenAPI: agent_api.OpenApiFunctionDefinition{
					Name:        "weather-api",
					Description: new("Weather lookup"),
				},
			},
			validate: func(t *testing.T, got any) {
				oa, ok := got.(agent_yaml.OpenApiTool)
				if !ok {
					t.Fatalf("expected agent_yaml.OpenApiTool, got %T", got)
				}
				if oa.Tool.Kind != agent_yaml.ToolKindOpenApi {
					t.Errorf("Kind = %q, want %q", oa.Tool.Kind, agent_yaml.ToolKindOpenApi)
				}
				if oa.Tool.Name != "openapi" {
					t.Errorf("Name = %q, want %q", oa.Tool.Name, "openapi")
				}
				if _, exists := oa.Options["openapi"]; !exists {
					t.Error("expected openapi in Options")
				}
			},
		},
		{
			name: "CodeInterpreterTool with container",
			input: agent_api.CodeInterpreterTool{
				Tool:      agent_api.Tool{Type: "code_interpreter"},
				Container: "container-id-123",
			},
			validate: func(t *testing.T, got any) {
				ci, ok := got.(agent_yaml.CodeInterpreterTool)
				if !ok {
					t.Fatalf("expected agent_yaml.CodeInterpreterTool, got %T", got)
				}
				if ci.Tool.Kind != agent_yaml.ToolKindCodeInterpreter {
					t.Errorf("Kind = %q, want %q", ci.Tool.Kind, agent_yaml.ToolKindCodeInterpreter)
				}
				if ci.Tool.Name != "code_interpreter" {
					t.Errorf("Name = %q, want %q", ci.Tool.Name, "code_interpreter")
				}
				if ci.Options["container"] != "container-id-123" {
					t.Errorf("container = %v, want %q", ci.Options["container"], "container-id-123")
				}
			},
		},
		{
			name: "CodeInterpreterTool without container",
			input: agent_api.CodeInterpreterTool{
				Tool:      agent_api.Tool{Type: "code_interpreter"},
				Container: nil,
			},
			validate: func(t *testing.T, got any) {
				ci, ok := got.(agent_yaml.CodeInterpreterTool)
				if !ok {
					t.Fatalf("expected agent_yaml.CodeInterpreterTool, got %T", got)
				}
				if _, exists := ci.Options["container"]; exists {
					t.Error("expected container to be absent when nil")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ConvertToolToYaml(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error %q does not contain %q", err, tc.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.validate != nil {
				tc.validate(t, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ConvertAgentDefinition
// ---------------------------------------------------------------------------

func TestConvertAgentDefinition(t *testing.T) {
	t.Parallel()

	t.Run("empty tools", func(t *testing.T) {
		t.Parallel()
		def := agent_api.PromptAgentDefinition{
			Model:        "gpt-4o",
			Instructions: new("Be helpful"),
		}

		got, err := ConvertAgentDefinition(def)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.AgentDefinition.Kind != agent_yaml.AgentKindPrompt {
			t.Errorf("Kind = %q, want %q", got.AgentDefinition.Kind, agent_yaml.AgentKindPrompt)
		}
		if got.Model.Id != "gpt-4o" {
			t.Errorf("Model.Id = %q, want %q", got.Model.Id, "gpt-4o")
		}
		if got.Instructions == nil || *got.Instructions != "Be helpful" {
			t.Errorf("Instructions mismatch")
		}
		if got.Tools == nil {
			t.Fatal("Tools should not be nil")
		}
		if len(*got.Tools) != 0 {
			t.Errorf("expected 0 tools, got %d", len(*got.Tools))
		}
	})

	t.Run("with tools", func(t *testing.T) {
		t.Parallel()
		def := agent_api.PromptAgentDefinition{
			Model:        "gpt-4o-mini",
			Instructions: new("Do things"),
			Tools: []any{
				agent_api.FunctionTool{
					Tool: agent_api.Tool{Type: "function"},
					Name: "fn1",
				},
				agent_api.CodeInterpreterTool{
					Tool: agent_api.Tool{Type: "code_interpreter"},
				},
			},
		}

		got, err := ConvertAgentDefinition(def)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(*got.Tools) != 2 {
			t.Fatalf("expected 2 tools, got %d", len(*got.Tools))
		}
		// Verify first tool is FunctionTool
		if _, ok := (*got.Tools)[0].(agent_yaml.FunctionTool); !ok {
			t.Errorf("expected first tool to be FunctionTool, got %T", (*got.Tools)[0])
		}
		// Verify second tool is CodeInterpreterTool
		if _, ok := (*got.Tools)[1].(agent_yaml.CodeInterpreterTool); !ok {
			t.Errorf("expected second tool to be CodeInterpreterTool, got %T", (*got.Tools)[1])
		}
	})

	t.Run("unsupported tool propagates error", func(t *testing.T) {
		t.Parallel()
		def := agent_api.PromptAgentDefinition{
			Model: "gpt-4o",
			Tools: []any{"bad-tool"},
		}
		_, err := ConvertAgentDefinition(def)
		if err == nil {
			t.Fatal("expected error for unsupported tool")
		}
		if !strings.Contains(err.Error(), "unsupported tool type") {
			t.Errorf("error %q does not mention unsupported tool type", err)
		}
	})

	t.Run("nil instructions", func(t *testing.T) {
		t.Parallel()
		def := agent_api.PromptAgentDefinition{
			Model: "gpt-4o",
		}
		got, err := ConvertAgentDefinition(def)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Instructions != nil {
			t.Errorf("Instructions = %v, want nil", got.Instructions)
		}
	})
}

// ---------------------------------------------------------------------------
// ConvertParameters
// ---------------------------------------------------------------------------

func TestConvertParameters(t *testing.T) {
	t.Parallel()

	t.Run("nil parameters returns nil", func(t *testing.T) {
		t.Parallel()
		got, err := ConvertParameters(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})

	t.Run("empty parameters returns nil", func(t *testing.T) {
		t.Parallel()
		got, err := ConvertParameters(map[string]OpenApiParameter{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})

	t.Run("single parameter with schema and enum", func(t *testing.T) {
		t.Parallel()
		params := map[string]OpenApiParameter{
			"region": {
				Description: "Azure region",
				Required:    true,
				Schema: &OpenApiSchema{
					Type: "string",
					Enum: []any{"eastus", "westus"},
				},
			},
		}

		got, err := ConvertParameters(params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil {
			t.Fatal("expected non-nil PropertySchema")
		}
		if len(got.Properties) != 1 {
			t.Fatalf("expected 1 property, got %d", len(got.Properties))
		}
		p := got.Properties[0]
		if p.Name != "region" {
			t.Errorf("Name = %q, want %q", p.Name, "region")
		}
		if p.Kind != "string" {
			t.Errorf("Kind = %q, want %q", p.Kind, "string")
		}
		if p.Description == nil || *p.Description != "Azure region" {
			t.Errorf("Description mismatch")
		}
		if p.Required == nil || *p.Required != true {
			t.Errorf("Required = %v, want true", p.Required)
		}
		if p.EnumValues == nil || len(*p.EnumValues) != 2 {
			t.Fatalf("expected 2 enum values, got %v", p.EnumValues)
		}
		if (*p.EnumValues)[0] != "eastus" || (*p.EnumValues)[1] != "westus" {
			t.Errorf("EnumValues = %v, want [eastus westus]", *p.EnumValues)
		}
	})

	t.Run("parameter without schema defaults to string kind", func(t *testing.T) {
		t.Parallel()
		params := map[string]OpenApiParameter{
			"name": {
				Description: "Agent name",
				Required:    false,
			},
		}

		got, err := ConvertParameters(params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil {
			t.Fatal("expected non-nil PropertySchema")
		}
		p := got.Properties[0]
		if p.Kind != "string" {
			t.Errorf("Kind = %q, want %q (default)", p.Kind, "string")
		}
		if p.EnumValues != nil {
			t.Errorf("EnumValues = %v, want nil", p.EnumValues)
		}
	})

	t.Run("parameter with example sets default", func(t *testing.T) {
		t.Parallel()
		params := map[string]OpenApiParameter{
			"timeout": {
				Description: "Timeout in seconds",
				Example:     30,
				Schema:      &OpenApiSchema{Type: "integer"},
			},
		}

		got, err := ConvertParameters(params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		p := got.Properties[0]
		if p.Default == nil {
			t.Fatal("expected Default to be set from Example")
		}
		if *p.Default != 30 {
			t.Errorf("Default = %v, want 30", *p.Default)
		}
	})
}

// ---------------------------------------------------------------------------
// MergeManifestIntoAgentDefinition
// ---------------------------------------------------------------------------

func TestMergeManifestIntoAgentDefinition(t *testing.T) {
	t.Parallel()

	t.Run("fills empty name from manifest", func(t *testing.T) {
		t.Parallel()
		manifest := &Manifest{
			Name:        "manifest-agent",
			DisplayName: "Manifest Agent",
			Description: "A description",
		}
		agentDef := &agent_yaml.AgentDefinition{
			Kind: agent_yaml.AgentKindPrompt,
		}

		result := MergeManifestIntoAgentDefinition(manifest, agentDef)

		if result.Name != "manifest-agent" {
			t.Errorf("Name = %q, want %q", result.Name, "manifest-agent")
		}
	})

	t.Run("does not overwrite existing name", func(t *testing.T) {
		t.Parallel()
		manifest := &Manifest{
			Name: "manifest-name",
		}
		agentDef := &agent_yaml.AgentDefinition{
			Kind: agent_yaml.AgentKindPrompt,
			Name: "existing-name",
		}

		result := MergeManifestIntoAgentDefinition(manifest, agentDef)

		if result.Name != "existing-name" {
			t.Errorf("Name = %q, want %q", result.Name, "existing-name")
		}
	})

	t.Run("does not modify original agent definition", func(t *testing.T) {
		t.Parallel()
		manifest := &Manifest{
			Name: "new-name",
		}
		agentDef := &agent_yaml.AgentDefinition{
			Kind: agent_yaml.AgentKindPrompt,
		}

		_ = MergeManifestIntoAgentDefinition(manifest, agentDef)

		if agentDef.Name != "" {
			t.Errorf("original AgentDefinition.Name was modified to %q", agentDef.Name)
		}
	})

	t.Run("preserves kind when already set", func(t *testing.T) {
		t.Parallel()
		manifest := &Manifest{
			Name: "test",
		}
		agentDef := &agent_yaml.AgentDefinition{
			Kind: agent_yaml.AgentKindPrompt,
			Name: "keep",
		}

		result := MergeManifestIntoAgentDefinition(manifest, agentDef)

		if result.Kind != agent_yaml.AgentKindPrompt {
			t.Errorf("Kind = %q, want %q", result.Kind, agent_yaml.AgentKindPrompt)
		}
	})
}

// ---------------------------------------------------------------------------
// injectParameterValues
// ---------------------------------------------------------------------------

func TestInjectParameterValues(t *testing.T) {
	t.Parallel()

	t.Run("replaces {{param}} style", func(t *testing.T) {
		t.Parallel()
		template := "Hello {{name}}, welcome to {{place}}!"
		values := ParameterValues{
			"name":  "Alice",
			"place": "Wonderland",
		}

		got, err := injectParameterValues(template, values)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "Hello Alice, welcome to Wonderland!"
		if string(got) != want {
			t.Errorf("got %q, want %q", string(got), want)
		}
	})

	t.Run("replaces {{ param }} style with spaces", func(t *testing.T) {
		t.Parallel()
		template := "Value is {{ apiKey }}"
		values := ParameterValues{
			"apiKey": "secret-123",
		}

		got, err := injectParameterValues(template, values)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "Value is secret-123"
		if string(got) != want {
			t.Errorf("got %q, want %q", string(got), want)
		}
	})

	t.Run("replaces both styles in same template", func(t *testing.T) {
		t.Parallel()
		template := "{{key1}} and {{ key1 }}"
		values := ParameterValues{
			"key1": "replaced",
		}

		got, err := injectParameterValues(template, values)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "replaced and replaced"
		if string(got) != want {
			t.Errorf("got %q, want %q", string(got), want)
		}
	})

	t.Run("no placeholders returns unchanged", func(t *testing.T) {
		t.Parallel()
		template := "no placeholders here"
		values := ParameterValues{"key": "val"}

		got, err := injectParameterValues(template, values)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(got) != template {
			t.Errorf("got %q, want %q", string(got), template)
		}
	})

	t.Run("empty parameter values returns unchanged", func(t *testing.T) {
		t.Parallel()
		template := "Hello {{name}}"
		values := ParameterValues{}

		got, err := injectParameterValues(template, values)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Unresolved placeholders remain but no error
		if string(got) != template {
			t.Errorf("got %q, want %q", string(got), template)
		}
	})

	t.Run("non-string value is converted via Sprintf", func(t *testing.T) {
		t.Parallel()
		template := "count={{count}}"
		values := ParameterValues{"count": 42}

		got, err := injectParameterValues(template, values)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "count=42"
		if string(got) != want {
			t.Errorf("got %q, want %q", string(got), want)
		}
	})

	t.Run("empty template returns empty", func(t *testing.T) {
		t.Parallel()
		got, err := injectParameterValues("", ParameterValues{"k": "v"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(got) != "" {
			t.Errorf("got %q, want empty", string(got))
		}
	})
}

// ---------------------------------------------------------------------------
// convertFloat32ToFloat64
// ---------------------------------------------------------------------------

func TestConvertFloat32ToFloat64(t *testing.T) {
	t.Parallel()

	t.Run("nil returns nil", func(t *testing.T) {
		t.Parallel()
		got := convertFloat32ToFloat64(nil)
		if got != nil {
			t.Errorf("expected nil, got %v", *got)
		}
	})

	t.Run("converts value", func(t *testing.T) {
		t.Parallel()
		f32 := float32(0.75)
		got := convertFloat32ToFloat64(&f32)
		if got == nil {
			t.Fatal("expected non-nil")
		}
		if *got != float64(f32) {
			t.Errorf("got %v, want %v", *got, float64(f32))
		}
	})

	t.Run("zero value", func(t *testing.T) {
		t.Parallel()
		f32 := float32(0)
		got := convertFloat32ToFloat64(&f32)
		if got == nil {
			t.Fatal("expected non-nil")
		}
		if *got != 0 {
			t.Errorf("got %v, want 0", *got)
		}
	})
}

// ---------------------------------------------------------------------------
// convertInt32ToInt
// ---------------------------------------------------------------------------

func TestConvertInt32ToInt(t *testing.T) {
	t.Parallel()

	t.Run("nil returns nil", func(t *testing.T) {
		t.Parallel()
		got := convertInt32ToInt(nil)
		if got != nil {
			t.Errorf("expected nil, got %v", *got)
		}
	})

	t.Run("converts value", func(t *testing.T) {
		t.Parallel()
		i32 := int32(42)
		got := convertInt32ToInt(&i32)
		if got == nil {
			t.Fatal("expected non-nil")
		}
		if *got != 42 {
			t.Errorf("got %d, want 42", *got)
		}
	})

	t.Run("zero value", func(t *testing.T) {
		t.Parallel()
		i32 := int32(0)
		got := convertInt32ToInt(&i32)
		if got == nil {
			t.Fatal("expected non-nil")
		}
		if *got != 0 {
			t.Errorf("got %d, want 0", *got)
		}
	})
}

// ---------------------------------------------------------------------------
// convertToPropertySchema
// ---------------------------------------------------------------------------

func TestConvertToPropertySchema(t *testing.T) {
	t.Parallel()

	t.Run("nil input returns empty properties", func(t *testing.T) {
		t.Parallel()
		got := convertToPropertySchema(nil)
		if len(got.Properties) != 0 {
			t.Errorf("expected 0 properties, got %d", len(got.Properties))
		}
	})

	t.Run("non-nil input returns empty properties", func(t *testing.T) {
		t.Parallel()
		// Current implementation is a placeholder that always returns empty properties
		got := convertToPropertySchema(map[string]any{"key": "value"})
		if len(got.Properties) != 0 {
			t.Errorf("expected 0 properties, got %d", len(got.Properties))
		}
	})
}

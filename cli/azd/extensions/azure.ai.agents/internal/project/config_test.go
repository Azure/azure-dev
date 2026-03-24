// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"
)

// TestServiceTargetAgentConfig_WithToolboxes tests MarshalStruct/UnmarshalStruct round-trip
// with the Toolboxes field populated.
func TestServiceTargetAgentConfig_WithToolboxes(t *testing.T) {
	original := ServiceTargetAgentConfig{
		Toolboxes: []Toolbox{
			{
				Name:        "echo-toolbox",
				Description: "A sample toolbox",
				Tools: []map[string]any{
					{
						"type":                  "mcp",
						"server_label":          "github",
						"server_url":            "https://api.example.com/mcp",
						"project_connection_id": "TestKey",
					},
				},
			},
		},
	}

	s, err := MarshalStruct(&original)
	if err != nil {
		t.Fatalf("MarshalStruct failed: %v", err)
	}

	var roundTripped ServiceTargetAgentConfig
	if err := UnmarshalStruct(s, &roundTripped); err != nil {
		t.Fatalf("UnmarshalStruct failed: %v", err)
	}

	if len(roundTripped.Toolboxes) != 1 {
		t.Fatalf("Expected 1 toolbox, got %d", len(roundTripped.Toolboxes))
	}

	tb := roundTripped.Toolboxes[0]
	if tb.Name != "echo-toolbox" {
		t.Errorf("Expected toolbox name 'echo-toolbox', got '%s'", tb.Name)
	}
	if tb.Description != "A sample toolbox" {
		t.Errorf("Expected description 'A sample toolbox', got '%s'", tb.Description)
	}
	if len(tb.Tools) != 1 {
		t.Fatalf("Expected 1 tool in toolbox, got %d", len(tb.Tools))
	}

	tool := tb.Tools[0]
	if tool["type"] != "mcp" {
		t.Errorf("Expected tool type 'mcp', got '%v'", tool["type"])
	}
	if tool["server_label"] != "github" {
		t.Errorf("Expected server_label 'github', got '%v'", tool["server_label"])
	}
}

// TestServiceTargetAgentConfig_EmptyToolboxes tests that an empty Toolboxes slice round-trips correctly.
func TestServiceTargetAgentConfig_EmptyToolboxes(t *testing.T) {
	original := ServiceTargetAgentConfig{
		Toolboxes: []Toolbox{},
	}

	s, err := MarshalStruct(&original)
	if err != nil {
		t.Fatalf("MarshalStruct failed: %v", err)
	}

	var roundTripped ServiceTargetAgentConfig
	if err := UnmarshalStruct(s, &roundTripped); err != nil {
		t.Fatalf("UnmarshalStruct failed: %v", err)
	}

	if len(roundTripped.Toolboxes) != 0 {
		t.Errorf("Expected 0 toolboxes, got %d", len(roundTripped.Toolboxes))
	}
}

// TestServiceTargetAgentConfig_MultipleToolboxes tests round-tripping multiple toolboxes.
func TestServiceTargetAgentConfig_MultipleToolboxes(t *testing.T) {
	original := ServiceTargetAgentConfig{
		Toolboxes: []Toolbox{
			{
				Name:        "toolbox-one",
				Description: "First toolbox",
				Tools: []map[string]any{
					{
						"type":                  "mcp",
						"server_label":          "server-a",
						"project_connection_id": "KeyA",
					},
				},
			},
			{
				Name:        "toolbox-two",
				Description: "Second toolbox",
				Tools: []map[string]any{
					{
						"type":                  "mcp",
						"server_label":          "server-b",
						"project_connection_id": "KeyB",
					},
					{
						"type":                  "mcp",
						"server_label":          "server-c",
						"project_connection_id": "KeyC",
					},
				},
			},
		},
	}

	s, err := MarshalStruct(&original)
	if err != nil {
		t.Fatalf("MarshalStruct failed: %v", err)
	}

	var roundTripped ServiceTargetAgentConfig
	if err := UnmarshalStruct(s, &roundTripped); err != nil {
		t.Fatalf("UnmarshalStruct failed: %v", err)
	}

	if len(roundTripped.Toolboxes) != 2 {
		t.Fatalf("Expected 2 toolboxes, got %d", len(roundTripped.Toolboxes))
	}

	if roundTripped.Toolboxes[0].Name != "toolbox-one" {
		t.Errorf("Expected first toolbox name 'toolbox-one', got '%s'", roundTripped.Toolboxes[0].Name)
	}

	if roundTripped.Toolboxes[1].Name != "toolbox-two" {
		t.Errorf("Expected second toolbox name 'toolbox-two', got '%s'", roundTripped.Toolboxes[1].Name)
	}

	if len(roundTripped.Toolboxes[1].Tools) != 2 {
		t.Errorf("Expected 2 tools in second toolbox, got %d", len(roundTripped.Toolboxes[1].Tools))
	}
}

// TestServiceTargetAgentConfig_WithOtherFields tests that Toolboxes coexists correctly
// alongside other ServiceTargetAgentConfig fields.
func TestServiceTargetAgentConfig_WithOtherFields(t *testing.T) {
	original := ServiceTargetAgentConfig{
		Environment: map[string]string{"KEY": "VALUE"},
		Deployments: []Deployment{
			{
				Name: "test-deployment",
				Model: DeploymentModel{
					Name:    "gpt-4.1-mini",
					Format:  "OpenAI",
					Version: "2025-04-14",
				},
				Sku: DeploymentSku{
					Name:     "Standard",
					Capacity: 10,
				},
			},
		},
		Toolboxes: []Toolbox{
			{
				Name:        "my-toolbox",
				Description: "Coexisting toolbox",
				Tools: []map[string]any{
					{
						"type":                  "mcp",
						"server_label":          "test-server",
						"project_connection_id": "TestConn",
					},
				},
			},
		},
	}

	s, err := MarshalStruct(&original)
	if err != nil {
		t.Fatalf("MarshalStruct failed: %v", err)
	}

	var roundTripped ServiceTargetAgentConfig
	if err := UnmarshalStruct(s, &roundTripped); err != nil {
		t.Fatalf("UnmarshalStruct failed: %v", err)
	}

	if roundTripped.Environment["KEY"] != "VALUE" {
		t.Errorf("Expected env KEY=VALUE, got '%s'", roundTripped.Environment["KEY"])
	}

	if len(roundTripped.Deployments) != 1 {
		t.Fatalf("Expected 1 deployment, got %d", len(roundTripped.Deployments))
	}

	if len(roundTripped.Toolboxes) != 1 {
		t.Fatalf("Expected 1 toolbox, got %d", len(roundTripped.Toolboxes))
	}

	if roundTripped.Toolboxes[0].Name != "my-toolbox" {
		t.Errorf("Expected toolbox name 'my-toolbox', got '%s'", roundTripped.Toolboxes[0].Name)
	}
}

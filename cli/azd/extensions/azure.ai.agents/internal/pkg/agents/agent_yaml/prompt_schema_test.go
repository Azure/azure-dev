// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestPromptAgent_ConnectionsRoundTrip verifies `connections:` contains only
// references to sibling azure.ai.connection services.
func TestPromptAgent_ConnectionsRoundTrip(t *testing.T) {
	yamlContent := []byte(`
kind: prompt
name: conn-agent
model: gpt-4.1-mini
instructions: You are helpful.
connections:
  - aisearch-conn
  - apikey-conn
`)

	var promptDef PromptAgent
	if err := yaml.Unmarshal(yamlContent, &promptDef); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(promptDef.Connections) != 2 {
		t.Fatalf("connections: got %d, want 2", len(promptDef.Connections))
	}

	if promptDef.Connections[0] != "aisearch-conn" || promptDef.Connections[1] != "apikey-conn" {
		t.Errorf("connections: got %+v", promptDef.Connections)
	}

	// Round-trip: marshal then unmarshal and confirm the count is preserved.
	data, err := yaml.Marshal(promptDef)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var again PromptAgent
	if err := yaml.Unmarshal(data, &again); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if len(again.Connections) != 2 {
		t.Fatalf("round-tripped connections: got %d, want 2", len(again.Connections))
	}
}

// TestExtractResourceDefinitions_FileKind verifies files remain agent-owned.
func TestExtractResourceDefinitions_FileKind(t *testing.T) {
	manifest := []byte(`
name: m
resources:
  - kind: file
    name: handbook
    path: files/handbook.pdf
    purpose: assistants
`)

	resources, err := ExtractResourceDefinitions(manifest)
	if err != nil {
		t.Fatalf("ExtractResourceDefinitions: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("resources: got %d, want 1", len(resources))
	}

	file, ok := resources[0].(FileResource)
	if !ok {
		t.Fatalf("resource[0]: got %T, want FileResource", resources[0])
	}
	if file.Kind != ResourceKindFile || file.Path != "files/handbook.pdf" {
		t.Errorf("file resource: got %+v", file)
	}
	if file.Purpose != "assistants" {
		t.Errorf("file purpose: got %q", file.Purpose)
	}
}

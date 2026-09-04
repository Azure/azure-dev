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

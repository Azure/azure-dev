// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestPromptAgent_ConnectionsRoundTrip verifies the prompt-agent `connections:`
// block parses into PromptAgent.Connections and round-trips through YAML.
func TestPromptAgent_ConnectionsRoundTrip(t *testing.T) {
	yamlContent := []byte(`
kind: prompt
name: conn-agent
model: gpt-4.1-mini
instructions: You are helpful.
connections:
  - name: aisearch-conn
    category: CognitiveSearch
    target: https://my-search.search.windows.net
    authType: Entra
  - name: apikey-conn
    category: RemoteTool
    authType: ApiKey
    credentials:
      key: ${SEARCH_API_KEY}
    provision: true
`)

	var promptDef PromptAgent
	if err := yaml.Unmarshal(yamlContent, &promptDef); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(promptDef.Connections) != 2 {
		t.Fatalf("connections: got %d, want 2", len(promptDef.Connections))
	}

	first := promptDef.Connections[0]
	if first.Name != "aisearch-conn" || first.Category != "CognitiveSearch" {
		t.Errorf("first connection: got %+v", first)
	}
	if first.Target != "https://my-search.search.windows.net" || first.AuthType != "Entra" {
		t.Errorf("first connection target/auth: got %+v", first)
	}

	second := promptDef.Connections[1]
	if second.AuthType != "ApiKey" || !second.Provision {
		t.Errorf("second connection: got %+v", second)
	}
	if second.Credentials["key"] != "${SEARCH_API_KEY}" {
		t.Errorf("second connection credentials: got %+v", second.Credentials)
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

// TestExtractResourceDefinitions_SkillAndFileKinds verifies the manifest parser
// recognizes the `skill` and `file` resource kinds and decodes them into their
// typed resources.
func TestExtractResourceDefinitions_SkillAndFileKinds(t *testing.T) {
	manifest := []byte(`
name: m
resources:
  - kind: skill
    name: agentdevcompute
    path: skills/agentdevcompute
    version: "1.2.0"
  - kind: file
    name: handbook
    path: files/handbook.pdf
    purpose: assistants
`)

	resources, err := ExtractResourceDefinitions(manifest)
	if err != nil {
		t.Fatalf("ExtractResourceDefinitions: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("resources: got %d, want 2", len(resources))
	}

	skill, ok := resources[0].(SkillResource)
	if !ok {
		t.Fatalf("resource[0]: got %T, want SkillResource", resources[0])
	}
	if skill.Kind != ResourceKindSkill || skill.Name != "agentdevcompute" {
		t.Errorf("skill resource: got %+v", skill)
	}
	if skill.Path != "skills/agentdevcompute" || skill.Version != "1.2.0" {
		t.Errorf("skill path/version: got %+v", skill)
	}

	file, ok := resources[1].(FileResource)
	if !ok {
		t.Fatalf("resource[1]: got %T, want FileResource", resources[1])
	}
	if file.Kind != ResourceKindFile || file.Path != "files/handbook.pdf" {
		t.Errorf("file resource: got %+v", file)
	}
	if file.Purpose != "assistants" {
		t.Errorf("file purpose: got %q", file.Purpose)
	}
}

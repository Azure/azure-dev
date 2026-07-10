// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
)

// fakeToolboxBuilder records calls and returns a fixed MCP url.
type fakeToolboxBuilder struct {
	mcpURL       string
	ensureCalls  int
	resolveCalls int
	lastSkills   []skillBundle
	lastRef      toolboxRef
}

func (b *fakeToolboxBuilder) EnsureToolbox(
	_ context.Context, _ string, skills []skillBundle,
) (string, error) {
	b.ensureCalls++
	b.lastSkills = skills
	if b.mcpURL == "" {
		b.mcpURL = "https://proj/toolboxes/agent/versions/1/mcp"
	}
	return b.mcpURL, nil
}

func (b *fakeToolboxBuilder) ResolveToolbox(_ context.Context, ref toolboxRef) (string, error) {
	b.resolveCalls++
	b.lastRef = ref
	if b.mcpURL == "" {
		b.mcpURL = "https://proj/toolboxes/existing/versions/2/mcp"
	}
	return b.mcpURL, nil
}

func writeSkillsDir(t *testing.T, skills map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if skills == nil {
		return dir
	}
	for name, skillMD := range skills {
		bundle := filepath.Join(dir, "skills", name)
		if err := os.MkdirAll(bundle, 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(bundle, "SKILL.md"), []byte(skillMD), 0o600); err != nil {
			t.Fatalf("write SKILL.md: %v", err)
		}
	}
	return dir
}

const validSkillMD = `---
name: agentdevcompute
description: Helps with dev compute tasks.
metadata:
  version: 1.2.0
---
# Body
Some skill instructions.
`

func TestParseSkillMD_Valid(t *testing.T) {
	dir := writeSkillsDir(t, map[string]string{"agentdevcompute": validSkillMD})
	meta, err := parseSkillMD(filepath.Join(dir, "skills", "agentdevcompute", "SKILL.md"))
	if err != nil {
		t.Fatalf("parseSkillMD: %v", err)
	}
	if meta.Name != "agentdevcompute" || meta.Description == "" || meta.Version != "1.2.0" {
		t.Errorf("meta: got %+v", meta)
	}
	if !strings.Contains(meta.Instructions, "Some skill instructions.") {
		t.Errorf("instructions body not captured: got %q", meta.Instructions)
	}
}

func TestParseSkillMD_VersionOptional(t *testing.T) {
	md := `---
name: s
description: has no version, which is allowed
---
body content
`
	dir := writeSkillsDir(t, map[string]string{"s": md})
	meta, err := parseSkillMD(filepath.Join(dir, "skills", "s", "SKILL.md"))
	if err != nil {
		t.Fatalf("version should be optional: %v", err)
	}
	if meta.Version != "" {
		t.Errorf("expected empty version, got %q", meta.Version)
	}
	if !strings.Contains(meta.Instructions, "body content") {
		t.Errorf("instructions: got %q", meta.Instructions)
	}
}

func TestParseSkillMD_MissingDescription(t *testing.T) {
	md := `---
name: s
metadata:
  version: 1.0.0
---
body
`
	dir := writeSkillsDir(t, map[string]string{"s": md})
	_, err := parseSkillMD(filepath.Join(dir, "skills", "s", "SKILL.md"))
	if err == nil || !strings.Contains(err.Error(), "description") {
		t.Fatalf("expected description error, got %v", err)
	}
}

func TestParseSkillMD_NoFrontmatter(t *testing.T) {
	md := "# Just a heading\nno frontmatter\n"
	dir := writeSkillsDir(t, map[string]string{"s": md})
	_, err := parseSkillMD(filepath.Join(dir, "skills", "s", "SKILL.md"))
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestScanSkillsDir_MultipleBundlesSorted(t *testing.T) {
	skillB := strings.Replace(validSkillMD, "agentdevcompute", "bravo", 1)
	skillA := strings.Replace(validSkillMD, "agentdevcompute", "alpha", 1)
	dir := writeSkillsDir(t, map[string]string{"bravo": skillB, "alpha": skillA})

	bundles, err := scanSkillsDir(dir)
	if err != nil {
		t.Fatalf("scanSkillsDir: %v", err)
	}
	if len(bundles) != 2 {
		t.Fatalf("bundles: got %d, want 2", len(bundles))
	}
	if bundles[0].Dir != "alpha" || bundles[1].Dir != "bravo" {
		t.Errorf("sort: got %s, %s", bundles[0].Dir, bundles[1].Dir)
	}
}

func TestScanSkillsDir_Empty(t *testing.T) {
	dir := writeSkillsDir(t, nil)
	bundles, err := scanSkillsDir(dir)
	if err != nil {
		t.Fatalf("scanSkillsDir: %v", err)
	}
	if bundles != nil {
		t.Errorf("expected nil for missing skills/, got %d", len(bundles))
	}
}

func TestInjectMcpTool_AddsWhenAbsent(t *testing.T) {
	managed := &agent_yaml.ManagedAgent{}
	injectMcpTool(managed, "toolbox-a", "https://proj/mcp")

	if len(managed.Tools) != 1 {
		t.Fatalf("tools: got %d, want 1", len(managed.Tools))
	}
	tool := managed.Tools[0].(map[string]any)
	if tool["type"] != "mcp" || tool["server_url"] != "https://proj/mcp" {
		t.Errorf("tool: got %+v", tool)
	}
}

func TestInjectMcpTool_NotDuplicated(t *testing.T) {
	managed := &agent_yaml.ManagedAgent{
		Tools: []any{
			map[string]any{"type": "mcp", "server_url": "https://proj/mcp"},
		},
	}
	injectMcpTool(managed, "toolbox-a", "https://proj/mcp")
	if len(managed.Tools) != 1 {
		t.Errorf("expected no duplicate mcp tool, got %d", len(managed.Tools))
	}
}

func TestToolboxNode_PrimaryRegistersSkills(t *testing.T) {
	managed := &agent_yaml.ManagedAgent{Model: "m", Instructions: "i"}
	managed.Name = "agent"
	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	fake := &fakeToolboxBuilder{}

	skills := []skillBundle{{Dir: "s", Meta: skillMeta{
		Name: "s", Description: "d", Version: "1.0.0", Instructions: "do the thing",
	}}}
	node := toolboxNode(g, skills, nil, func() (toolboxBuilder, error) { return fake, nil })
	if node == nil {
		t.Fatal("expected a toolbox node")
	}
	if err := node.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := node.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if fake.ensureCalls != 1 || fake.resolveCalls != 0 {
		t.Errorf("expected 1 ensure, 0 resolve; got %d, %d", fake.ensureCalls, fake.resolveCalls)
	}
	if g.bindings[toolboxMcpURLBindingKey] == nil {
		t.Error("expected toolbox_mcp_url binding")
	}
	if len(managed.Tools) != 1 || managed.Tools[0].(map[string]any)["type"] != "mcp" {
		t.Errorf("expected mcp tool, got %+v", managed.Tools)
	}
}

func TestToolboxNode_FallbackReferenceExisting(t *testing.T) {
	managed := &agent_yaml.ManagedAgent{Model: "m", Instructions: "i"}
	managed.Name = "agent"
	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	fake := &fakeToolboxBuilder{}

	ref := &agent_yaml.ToolboxReference{Name: "existing-tb", Version: "2"}
	node := toolboxNode(g, nil, ref, func() (toolboxBuilder, error) { return fake, nil })
	if node == nil {
		t.Fatal("expected a toolbox node")
	}
	if err := node.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := node.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if fake.resolveCalls != 1 || fake.ensureCalls != 0 {
		t.Errorf("expected 1 resolve, 0 ensure; got %d, %d", fake.resolveCalls, fake.ensureCalls)
	}
	if fake.lastRef.Name != "existing-tb" || fake.lastRef.Version != "2" {
		t.Errorf("ref: got %+v", fake.lastRef)
	}
	if len(managed.Tools) != 1 {
		t.Errorf("expected mcp tool attached, got %+v", managed.Tools)
	}
}

func TestToolboxNode_NoneReturnsNil(t *testing.T) {
	g := &promptGraph{managed: &agent_yaml.ManagedAgent{}, bindings: map[string]any{}}
	node := toolboxNode(g, nil, nil, func() (toolboxBuilder, error) { return nil, nil })
	if node != nil {
		t.Fatal("expected nil node when no skills and no reference")
	}
}
